// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gavv/emacs-jail-mcp/internal/config"
	"github.com/gavv/emacs-jail-mcp/internal/display"
)

// EntrypointEnv returns the environment variables to pass to podman run --env
// so the entrypoint subcommand can configure itself inside the container.
func EntrypointEnv(cfg *config.ServerConfig, disp *display.Display) []string {
	// Resolve symlinks on the host so a NixOS profile symlink becomes the
	// canonical /nix/store path, which is visible inside the container via
	// the /nix/store volume mount and runs the host emacs binary.
	emacsBinary := config.ResolveEmacsBinary(cfg.EmacsBinary)
	return []string{
		"EMACS_JAIL_LOCK_PATH=" + cfg.LockPath(),
		"EMACS_JAIL_SOCKET_PATH=" + cfg.SocketPath(),
		"EMACS_JAIL_LOG_PATH=" + cfg.LogPath(),
		"EMACS_JAIL_DISPLAY=" + disp.String(),
		fmt.Sprintf("EMACS_JAIL_DISPLAY_NUMBER=%d", disp.Number),
		fmt.Sprintf("EMACS_JAIL_DISPLAY_WIDTH=%d", disp.Width),
		fmt.Sprintf("EMACS_JAIL_DISPLAY_HEIGHT=%d", disp.Height),
		fmt.Sprintf("EMACS_JAIL_DISPLAY_DEPTH=%d", disp.Depth),
		"EMACS_JAIL_ELISP_DIR=" + cfg.ElispDir(),
		"EMACS_JAIL_EMACS_BINARY=" + emacsBinary,
		"EMACS_JAIL_EMACS_LAUNCHER=" + cfg.EmacsLauncher,
		"EMACS_JAIL_PRE_INIT_FILE=" + cfg.EmacsPreInitFile,
		"EMACS_JAIL_POST_INIT_FILE=" + cfg.EmacsPostInitFile,
		fmt.Sprintf("EMACS_JAIL_SWALLOW_ERRORS=%t", cfg.SwallowErrors),
		"EMACS_JAIL_STDERR_PATH=" + cfg.StderrPath(),
	}
}

// Entrypoint holds the configuration read from environment variables inside
// the container. Use NewEntrypoint to construct it from the process environment.
type Entrypoint struct {
	LockPath      string
	SocketPath    string
	LogPath       string
	StderrPath    string
	Display       string
	DisplayNumber string
	DisplayWidth  string
	DisplayHeight string
	DisplayDepth  string
	ElispDir      string
	EmacsBinary   string
	EmacsLauncher string
	PreInitFile   string
	PostInitFile  string
	SwallowErrors bool
}

// NewEntrypoint reads the environment variables set by the host and returns
// an Entrypoint ready to run inside the container.
func NewEntrypoint() *Entrypoint {
	return &Entrypoint{
		LockPath:      os.Getenv("EMACS_JAIL_LOCK_PATH"),
		SocketPath:    os.Getenv("EMACS_JAIL_SOCKET_PATH"),
		LogPath:       os.Getenv("EMACS_JAIL_LOG_PATH"),
		StderrPath:    os.Getenv("EMACS_JAIL_STDERR_PATH"),
		Display:       os.Getenv("EMACS_JAIL_DISPLAY"),
		DisplayNumber: os.Getenv("EMACS_JAIL_DISPLAY_NUMBER"),
		DisplayWidth:  os.Getenv("EMACS_JAIL_DISPLAY_WIDTH"),
		DisplayHeight: os.Getenv("EMACS_JAIL_DISPLAY_HEIGHT"),
		DisplayDepth:  os.Getenv("EMACS_JAIL_DISPLAY_DEPTH"),
		ElispDir:      os.Getenv("EMACS_JAIL_ELISP_DIR"),
		EmacsBinary:   os.Getenv("EMACS_JAIL_EMACS_BINARY"),
		EmacsLauncher: os.Getenv("EMACS_JAIL_EMACS_LAUNCHER"),
		PreInitFile:   os.Getenv("EMACS_JAIL_PRE_INIT_FILE"),
		PostInitFile:  os.Getenv("EMACS_JAIL_POST_INIT_FILE"),
		SwallowErrors: os.Getenv("EMACS_JAIL_SWALLOW_ERRORS") == "true",
	}
}

// Run executes the full entrypoint sequence inside the container:
// starts the watchdog, launches Xvfb, waits for the X11 socket,
// sets up the environment, removes stale files, and exec's Emacs.
func (e *Entrypoint) Run() error {
	e.startWatchdog()

	if err := e.startXvfb(); err != nil {
		return err
	}
	if err := e.waitForX11Socket(); err != nil {
		return err
	}

	if err := os.Setenv("DISPLAY", e.Display); err != nil {
		return err
	}
	if err := os.Setenv("EMACSLOADPATH", e.ElispDir+":"); err != nil {
		return err
	}
	if err := os.Setenv("EMACS_JAIL_LOG_PATH", e.LogPath); err != nil {
		return err
	}

	_ = os.Remove(e.SocketPath)
	_ = os.Remove(e.LogPath)
	_ = os.Remove(e.StderrPath)

	return e.execEmacs()
}

func (e *Entrypoint) startWatchdog() {
	lockPath := e.LockPath
	socketPath := e.SocketPath
	logPath := e.LogPath
	stderrPath := e.StderrPath
	pgid := syscall.Getpgrp()

	go func() {
		for {
			lf, err := os.Open(lockPath)
			if err == nil {
				lockErr := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
				_ = lf.Close()
				if lockErr == nil {
					_ = os.Remove(socketPath)
					_ = os.Remove(logPath)
					_ = os.Remove(stderrPath)
					_ = syscall.Kill(-pgid, syscall.SIGTERM)
					return
				}
			}
			time.Sleep(time.Second)
		}
	}()
}

func (e *Entrypoint) startXvfb() error {
	screen := e.DisplayWidth + "x" + e.DisplayHeight + "x" + e.DisplayDepth
	cmd := exec.Command("Xvfb", e.Display, "-screen", "0", screen, "-nolisten", "tcp", "-ac")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start Xvfb: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	return nil
}

func (e *Entrypoint) waitForX11Socket() error {
	path := "/tmp/.X11-unix/X" + e.DisplayNumber
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("wait for X11 socket %s: timed out", path)
}

func (e *Entrypoint) execEmacs() error {
	program := e.EmacsBinary
	if program == "" {
		program = "emacs"
	}
	args := []string{program, "--maximized", "--eval", e.rpcStartExpression()}
	if e.EmacsLauncher != "" {
		program = e.EmacsLauncher
		args = append([]string{e.EmacsLauncher}, args...)
	}

	stderrFile, err := os.Create(e.StderrPath)
	if err != nil {
		return fmt.Errorf("create stderr file: %w", err)
	}
	if err := syscall.Dup2(int(stderrFile.Fd()), syscall.Stderr); err != nil {
		_ = stderrFile.Close()
		return fmt.Errorf("redirect stderr: %w", err)
	}
	_ = stderrFile.Close()

	path, err := exec.LookPath(program)
	// Fall back to the container emacs only for bare binary names. An
	// absolute host path (e.g. /nix/store/... on NixOS) must not silently
	// fall back, otherwise a missing mount would hide the misconfiguration.
	if err != nil && e.EmacsLauncher == "" && !filepath.IsAbs(program) &&
		program != "emacs" {
		program = "emacs"
		args[0] = "emacs"
		path, err = exec.LookPath("emacs")
	}
	if err != nil {
		return fmt.Errorf("find %s: %w", program, err)
	}
	return syscall.Exec(path, args, os.Environ())
}

func (e *Entrypoint) rpcStartExpression() string {
	parts := []string{
		"progn",
		fmt.Sprintf("(add-to-list (quote load-path) %q)", e.ElispDir),
		"(require (quote emacs-jail-rpc))",
	}
	if e.PostInitFile != "" {
		parts = append(parts, fmt.Sprintf("(load %q)", e.PostInitFile))
	}
	parts = append(parts, fmt.Sprintf("(emacs-jail-rpc-start %q)", e.SocketPath))
	return "(" + strings.Join(parts, "\n  ") + ")"
}
