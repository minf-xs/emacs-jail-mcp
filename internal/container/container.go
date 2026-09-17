// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package container

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	logging "github.com/op/go-logging"

	"github.com/gavv/emacs-jail-mcp/internal/config"
	"github.com/gavv/emacs-jail-mcp/internal/display"
)

var log = logging.MustGetLogger("container")

type Container struct {
	serverConfig *config.ServerConfig
	display      *display.Display
	mu           sync.Mutex
	running      bool
	lockFile     *os.File // held open with LOCK_EX so watchdog detects parent death
}

func New(serverConfig *config.ServerConfig, disp *display.Display) *Container {
	return &Container{serverConfig: serverConfig, display: disp}
}

func (c *Container) Start(ctx context.Context) error {
	if c.running {
		return fmt.Errorf("container %q is already running",
			c.serverConfig.ContainerName())
	}

	log.Infof("starting container %q", c.serverConfig.ContainerName())

	// Write elisp files to /tmp (shared with the container via /tmp:/tmp mount).
	if err := WriteElispFiles(c.serverConfig.ElispDir()); err != nil {
		log.Errorf("failed to write elisp files: %v", err)
		return fmt.Errorf("write elisp files: %w", err)
	}
	log.Infof("wrote elisp files to %s", c.serverConfig.ElispDir())

	// Create and hold an exclusive flock on a sentinel file in /tmp. The watchdog
	// inside the container polls this lock via "flock --nonblock". When this process
	// exits (for any reason, including SIGKILL), the OS releases the lock and the
	// watchdog can detect it without needing to see the host PID namespace.
	lockFile, err := os.OpenFile(c.serverConfig.LockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Errorf("failed to create lock file %s: %v", c.serverConfig.LockPath(), err)
		_ = os.RemoveAll(c.serverConfig.ElispDir())
		return fmt.Errorf("create lock file: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		log.Errorf("failed to acquire lock %s: %v", c.serverConfig.LockPath(), err)
		_ = lockFile.Close()
		_ = os.RemoveAll(c.serverConfig.ElispDir())
		return fmt.Errorf("acquire lock: %w", err)
	}
	c.lockFile = lockFile
	log.Infof("acquired lock %s", c.serverConfig.LockPath())

	envVars := EntrypointEnv(c.serverConfig, c.display)
	uid := os.Getuid()
	gid := os.Getgid()
	cwd, err := os.Getwd()
	if err != nil {
		log.Errorf("failed to get working directory: %v", err)
		_ = c.lockFile.Close()
		c.lockFile = nil
		_ = os.RemoveAll(c.serverConfig.ElispDir())
		return fmt.Errorf("get working directory: %w", err)
	}
	entrypointBinary, err := os.Executable()
	if err != nil {
		log.Errorf("failed to get executable path: %v", err)
		_ = c.lockFile.Close()
		c.lockFile = nil
		_ = os.RemoveAll(c.serverConfig.ElispDir())
		return fmt.Errorf("get executable path: %w", err)
	}

	args := []string{
		"run",
		"--rm", "--detach", "--tty", "--init",
		"--name", c.serverConfig.ContainerName(),
		"--workdir", cwd,
		"--uts=host",
		"--network=host",
		"--ipc=host",
		"--volume", "/tmp:/tmp",
		"--security-opt", "label=disable",
	}
	if c.serverConfig.UseSudo {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
	}
	args = c.appendVolumeIfExists(args,
		fmt.Sprintf("/run/user/%d", uid), fmt.Sprintf("/run/user/%d", uid))
	if home := os.Getenv("HOME"); home != "" {
		args = append(args, "--volume", fmt.Sprintf("%s:%s:O", home, home))
		args = c.appendVolumeIfExists(args, filepath.Join(home, ".cache"),
			filepath.Join(home, ".cache"))
	}
	args = c.appendVolumeIfExists(args, "/nix/store", "/nix/store:ro")
	args = c.appendVolumeIfExists(args,
		"/etc/fonts/conf.d/00-nixos-cache.conf",
		"/etc/fonts/conf.d/00-nixos-cache.conf:ro")
	args = c.appendVolumeIfExists(args, entrypointBinary, entrypointBinary+":ro")
	for _, kv := range envVars {
		args = append(args, "--env", kv)
	}
	if home := os.Getenv("HOME"); home != "" {
		args = append(args, "--env", "HOME="+home)
	}
	path := os.Getenv("PATH")
	if path != "" {
		path += ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	} else {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	args = append(args, "--env", "PATH="+path)
	shell := os.Getenv("SHELL")
	if shell == "" || !filepath.IsAbs(shell) ||
		strings.HasPrefix(shell, "/nix/store") ||
		strings.HasPrefix(shell, "/run/current-system") {
		shell = "/bin/bash"
	}
	args = append(args, "--env", "SHELL="+shell)
	image := c.serverConfig.ContainerImage
	if image == "" {
		image = "emacs-jail:latest"
	}
	args = append(args,
		image,
		entrypointBinary, "entrypoint",
	)

	out, err := c.podman(ctx, args...)
	if err != nil {
		log.Errorf("podman run failed: %v (output: %s)", err, strings.TrimSpace(out))
		_ = c.lockFile.Close()
		c.lockFile = nil
		_ = os.RemoveAll(c.serverConfig.ElispDir())
		return fmt.Errorf("podman run: %w\noutput: %s", err, out)
	}

	c.mu.Lock()
	c.running = true
	c.mu.Unlock()
	log.Infof("container %q started (id=%s)",
		c.serverConfig.ContainerName(), strings.TrimSpace(out))
	return nil
}

func (c *Container) Stop(ctx context.Context) error {
	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if !running {
		return fmt.Errorf("container %q is not running",
			c.serverConfig.ContainerName())
	}

	log.Infof("stopping container %q", c.serverConfig.ContainerName())

	out, err := c.podman(ctx, "kill", c.serverConfig.ContainerName())
	if err != nil {
		// The container may have already exited on its own (e.g. Emacs called
		// kill-emacs). Treat this as a warning: cleanup still proceeds so the
		// lock, elisp dir, and Xvfb state are released regardless.
		log.Warningf("podman kill failed: %v (output: %s)", err, strings.TrimSpace(out))
	}

	// Forcibly remove the container so its name is freed immediately. This
	// matters for restart: the same container name is reused and "podman run"
	// fails if the previous container is still in "exiting" state.
	// --ignore makes this a no-op if the container is already gone.
	rmOut, rmErr := c.podman(ctx, "rm", "--force", "--ignore",
		c.serverConfig.ContainerName())
	if rmErr != nil {
		log.Warningf("podman rm failed: %v (output: %s)", rmErr, strings.TrimSpace(rmOut))
	}

	// Release the flock sentinel so the watchdog inside the container can
	// detect that the parent process has gone. The OS releases the lock when
	// the file descriptor is closed.
	if c.lockFile != nil {
		_ = c.lockFile.Close()
		_ = os.Remove(c.serverConfig.LockPath())
		c.lockFile = nil
		log.Infof("released lock %s", c.serverConfig.LockPath())
	}

	_ = os.RemoveAll(c.serverConfig.ElispDir())

	// Remove stale Xvfb X11 socket and lock files so the next start can
	// bind to the same display number without "display already in use" errors.
	_ = os.Remove(c.display.X11SocketPath())
	_ = os.Remove(c.display.X11LockPath())

	c.mu.Lock()
	c.running = false
	c.mu.Unlock()

	if err != nil {
		log.Errorf("jail stop had error: %v", err)
		return fmt.Errorf("podman kill: %w\noutput: %s", err, out)
	}
	log.Infof("container %q stopped", c.serverConfig.ContainerName())
	return nil
}

// Wait blocks until the container process exits or ctx is cancelled.
// Returns nil when the container exits (for any reason), ctx.Err() on cancellation.
func (c *Container) Wait(ctx context.Context) error {
	_, err := c.podman(ctx, "wait", c.serverConfig.ContainerName())
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("podman wait: %w", err)
	}
	return nil
}

func (c *Container) Exec(ctx context.Context, cmd string, env ...string) (string, error) {
	log.Debugf("exec in container %q: %s", c.serverConfig.ContainerName(), cmd)

	args := []string{"exec", "--tty"}
	for _, kv := range env {
		args = append(args, "-e", kv)
	}
	args = append(args,
		c.serverConfig.ContainerName(), "/bin/bash", "-c", cmd)
	out, err := c.podman(ctx, args...)
	if err != nil {
		log.Errorf("exec failed: %v (output: %s)",
			err, strings.TrimSpace(out))
		return "", fmt.Errorf(
			"exec %q: %w\noutput: %s", cmd, err, out)
	}

	log.Debugf("exec completed (%d bytes output)", len(out))
	return out, nil
}

// Stderr is discarded.
func (c *Container) ExecRaw(
	ctx context.Context, cmd string,
) ([]byte, error) {
	log.Debugf("exec raw in container %q: %s", c.serverConfig.ContainerName(), cmd)

	args := c.podmanArgs(
		"exec", c.serverConfig.ContainerName(),
		"/bin/bash", "-c", cmd)
	log.Debugf("running: %s", strings.Join(args, " "))
	command := exec.CommandContext(ctx, args[0], args[1:]...)

	var stdout bytes.Buffer
	command.Stdout = &stdout

	if err := command.Run(); err != nil {
		log.Errorf("exec raw failed: %v", err)
		return nil, fmt.Errorf("exec raw %q: %w", cmd, err)
	}

	log.Debugf("exec raw completed (%d bytes output)", stdout.Len())
	return stdout.Bytes(), nil
}

func (c *Container) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *Container) appendVolumeIfExists(
	args []string, hostPath, containerPath string,
) []string {
	if _, err := os.Stat(hostPath); err != nil {
		if !os.IsNotExist(err) {
			log.Warningf("skipping volume %s: %v", hostPath, err)
		}
		return args
	}
	return append(args, "--volume", fmt.Sprintf("%s:%s", hostPath, containerPath))
}

// If n <= 0, all lines are returned.
// The log file lives in the shared /tmp mount and is read directly from the
// host filesystem, so this works even before the container is fully started.
// Returns an empty slice if the file does not exist yet.
func (c *Container) LogLines(n int) []string {
	data, err := os.ReadFile(c.serverConfig.LogPath())
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// If n <= 0, all lines are returned.
// The file lives in the shared /tmp mount and is read directly from the
// host filesystem. Returns an empty slice if the file does not exist yet.
func (c *Container) StderrLines(n int) []string {
	data, err := os.ReadFile(c.serverConfig.StderrPath())
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func (c *Container) podman(ctx context.Context, args ...string) (string, error) {
	fullArgs := c.podmanArgs(args...)
	log.Debugf("running: %s", c.formatCommand(fullArgs))
	cmd := exec.CommandContext(ctx, fullArgs[0], fullArgs[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Debugf("command failed: %v", err)
	}
	return string(out), err
}

func (c *Container) podmanArgs(args ...string) []string {
	var full []string
	if c.serverConfig.UseSudo {
		full = append(full, "sudo")
	}
	full = append(full, c.serverConfig.PodmanBinary)
	full = append(full, args...)
	return full
}

func (c *Container) formatCommand(args []string) string {
	return strings.Join(args, " ")
}
