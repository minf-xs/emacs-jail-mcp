// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package jail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	logging "github.com/op/go-logging"

	"github.com/gavv/emacs-jail-mcp/internal/config"
	"github.com/gavv/emacs-jail-mcp/internal/container"
	"github.com/gavv/emacs-jail-mcp/internal/display"
	"github.com/gavv/emacs-jail-mcp/internal/emacsclient"
)

var (
	log      = logging.MustGetLogger("jail")
	emacsLog = logging.MustGetLogger("emacs")
)

var (
	errNotRunning = errors.New(
		"jail is not running; call control with action=start first")
	errEmacsNotReady     = errors.New("jail is starting; emacs is not ready yet")
	errContainerNotReady = errors.New("jail container is not ready yet")
)

type State int

const (
	StateStopped State = iota
	StateStarting
	StateRunning
	StateStopping
)

func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	default:
		return "unknown"
	}
}

// containerBackend abstracts the container for testability.
type containerBackend interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Wait(ctx context.Context) error
	Exec(ctx context.Context, cmd string, env ...string) (string, error)
	ExecRaw(ctx context.Context, cmd string) ([]byte, error)
	IsRunning() bool
	LogLines(n int) []string
	StderrLines(n int) []string
}

// emacsClient abstracts the Emacs eval client for testability.
type emacsClient interface {
	Connect(ctx context.Context) error
	EvalElisp(ctx context.Context, expr string) (string, error)
	Close() error
}

// Jail orchestrates a Podman container running Emacs, Xvfb, and emacs-jail-rpc.
// All public methods are safe for concurrent use.
type Jail struct {
	mu           sync.RWMutex
	serverConfig *config.ServerConfig
	display      *display.Display
	backend      containerBackend
	client       emacsClient
	state        State
	logLines     []string
	stderrLines  []string
	stderrTail   *stderrStreamer
	startCtx     context.Context
	startCancel  context.CancelFunc
	startDone    chan error
}

func New(cfg *config.ServerConfig) *Jail {
	disp := display.New(cfg.Display)
	return &Jail{
		serverConfig: cfg,
		display:      disp,
		backend:      container.New(cfg, disp),
		state:        StateStopped,
	}
}

func (j *Jail) Start(ctx context.Context, timeout time.Duration, swallowErrors bool) error {
	startDone, started := j.beginStart(ctx, timeout, swallowErrors)
	if !started {
		return j.waitForStart(ctx, timeout, startDone)
	}
	return j.runStart(startDone, swallowErrors)
}

func (j *Jail) beginStart(
	ctx context.Context,
	timeout time.Duration,
	swallowErrors bool,
) (chan error, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.state == StateRunning {
		done := make(chan error, 1)
		done <- nil
		close(done)
		return done, false
	}
	if j.state == StateStarting {
		return j.startDone, false
	}

	if timeout <= 0 {
		timeout = j.serverConfig.StartTimeout
	}
	startCtx, cancel := context.WithTimeout(ctx, timeout)
	j.startCtx = startCtx
	j.startCancel = cancel
	j.startDone = make(chan error, 1)
	j.state = StateStarting
	j.serverConfig.SwallowErrors = swallowErrors
	log.Infof("starting jail (state: stopped -> starting)")
	return j.startDone, true
}

func (j *Jail) waitForStart(
	ctx context.Context,
	timeout time.Duration,
	startDone <-chan error,
) error {

	if timeout <= 0 {
		timeout = j.serverConfig.StartTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case err := <-startDone:
		return err
	case <-waitCtx.Done():
		return waitCtx.Err()
	}
}

func (j *Jail) runStart(startDone chan error, _ bool) error {
	startCtx, cancel := j.startContext()
	defer cancel()

	err := j.startContainer(startCtx)
	if err == nil {
		err = j.startStderrStreaming()
	}
	if err == nil {
		err = j.waitForSocket(startCtx)
	}
	if err == nil {
		err = j.connectClient(startCtx)
	}
	if err != nil {
		err = j.finishStartFailed(err)
	} else {
		err = j.finishStartSuccess()
	}

	startDone <- err
	close(startDone)
	return err
}

func (j *Jail) startContext() (context.Context, context.CancelFunc) {
	j.mu.RLock()
	ctx := j.startCtx
	cancel := j.startCancel
	j.mu.RUnlock()
	if ctx == nil {
		return context.Background(), func() {}
	}
	return ctx, func() {
		if cancel != nil {
			cancel()
		}
	}
}

func (j *Jail) startContainer(ctx context.Context) error {
	if err := j.backend.Start(ctx); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

func (j *Jail) startStderrStreaming() error {
	log.Infof("starting emacs stderr streaming from %s", j.serverConfig.StderrPath())
	tail := startStderrStreamer(context.Background(), j.serverConfig.StderrPath(),
		func(line string) {
			emacsLog.Debugf("%s", line)
		})
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state != StateStarting {
		tail.stop()
		return fmt.Errorf("start canceled")
	}
	j.stderrTail = tail
	return nil
}

func (j *Jail) connectClient(ctx context.Context) error {
	client := emacsclient.NewClient(j.serverConfig.SocketPath())
	log.Infof("connecting emacs client to %s", j.serverConfig.SocketPath())
	if err := client.Connect(ctx); err != nil {
		_ = client.Close()
		log.Errorf("emacs client connect failed: %v", err)
		return fmt.Errorf("connect to emacs: %w", err)
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state != StateStarting {
		_ = client.Close()
		return fmt.Errorf("start canceled")
	}
	j.client = client
	return nil
}

func (j *Jail) finishStartSuccess() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state != StateStarting {
		return fmt.Errorf("start canceled")
	}
	j.startCtx = nil
	j.startCancel = nil
	j.state = StateRunning
	log.Infof("jail is now running")
	return nil
}

func (j *Jail) finishStartFailed(err error) error {
	j.mu.Lock()
	wasStarting := j.state == StateStarting
	if wasStarting {
		j.state = StateStopped
		j.startCtx = nil
		j.startCancel = nil
		j.client = nil
	}
	j.mu.Unlock()
	if wasStarting {
		j.stopTailAndContainer()
	}
	log.Errorf("jail start failed: %v", err)
	return err
}

func (j *Jail) Stop(ctx context.Context) error {
	state, cancel, tail, client := j.beginStop()
	if state != StateRunning && state != StateStarting {
		return fmt.Errorf("jail is not running (state: %s)", state)
	}
	return j.runStop(ctx, cancel, tail, client)
}

func (j *Jail) beginStop() (State, context.CancelFunc, *stderrStreamer, emacsClient) {
	j.mu.Lock()
	defer j.mu.Unlock()
	state := j.state
	if state != StateRunning && state != StateStarting {
		return state, nil, nil, nil
	}
	log.Infof("stopping jail (state: %s -> stopping)", j.state)
	j.state = StateStopping
	cancel := j.startCancel
	j.startCtx = nil
	j.startCancel = nil
	tail := j.stderrTail
	j.stderrTail = nil
	client := j.client
	j.client = nil
	return state, cancel, tail, client
}

func (j *Jail) runStop(
	ctx context.Context,
	cancel context.CancelFunc,
	tail *stderrStreamer,
	client emacsClient,
) error {
	if cancel != nil {
		cancel()
	}

	var firstErr error
	if tail != nil {
		tail.stop()
	}
	if client != nil {
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close client: %w", err)
		}
	}

	stopCtx, cancelStop := context.WithTimeout(ctx, j.serverConfig.StopTimeout)
	defer cancelStop()
	if err := j.backend.Stop(stopCtx); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("stop container: %w", err)
	}

	j.finishStop(firstErr)
	return firstErr
}

func (j *Jail) finishStop(firstErr error) {
	j.mu.Lock()
	j.state = StateStopped
	j.mu.Unlock()

	if firstErr != nil {
		log.Errorf("jail stop had error: %v", firstErr)
	} else {
		log.Infof("jail stopped")
	}
}

func (j *Jail) Restart(ctx context.Context, timeout time.Duration, swallowErrors bool) error {
	if err := j.Stop(ctx); err != nil {
		return fmt.Errorf("restart: stop failed: %w", err)
	}
	j.clearLogs()
	if err := j.Start(ctx, timeout, swallowErrors); err != nil {
		return fmt.Errorf("restart: start failed (jail is now stopped): %w", err)
	}
	log.Infof("jail restarted successfully")
	return nil
}

func (j *Jail) clearLogs() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.logLines = nil
	j.stderrLines = nil
}

func (j *Jail) LogLines() []string {
	j.collectLogLines()
	j.mu.RLock()
	defer j.mu.RUnlock()
	return append([]string(nil), j.logLines...)
}

func (j *Jail) StderrLines() []string {
	j.collectStderrLines()
	j.mu.RLock()
	defer j.mu.RUnlock()
	return append([]string(nil), j.stderrLines...)
}

func (j *Jail) EvalElisp(ctx context.Context, expr string) (string, error) {
	state, client := j.evalSnapshot()
	if state == StateStarting {
		log.Warningf("eval rejected: jail is %s", state)
		return "", errEmacsNotReady
	}
	if state != StateRunning || client == nil {
		log.Warningf("eval rejected: jail is %s", state)
		return "", errNotRunning
	}

	log.Debugf("evaluating elisp (%d bytes)", len(expr))
	evalCtx, cancel := context.WithTimeout(ctx, j.serverConfig.ExecTimeout)
	defer cancel()
	result, err := client.EvalElisp(evalCtx, expr)
	if err != nil {
		log.Errorf("eval failed: %v", err)
		return "", err
	}
	log.Debugf("eval completed (%d bytes result)", len(result))
	return result, nil
}

func (j *Jail) evalSnapshot() (State, emacsClient) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.state, j.client
}

// Shell runs a shell command inside the container and returns its combined output.
// DISPLAY is passed explicitly so the command can interact with Xvfb.
func (j *Jail) Shell(ctx context.Context, cmd string) (string, error) {
	state, backend, display := j.containerSnapshot()
	if state != StateRunning && state != StateStarting {
		log.Warningf("shell rejected: jail is %s", state)
		return "", errNotRunning
	}
	if !backend.IsRunning() {
		log.Warningf("shell rejected: container is not ready")
		return "", errContainerNotReady
	}

	log.Debugf("running shell command: %s", cmd)
	shellCtx, cancel := context.WithTimeout(ctx, j.serverConfig.ExecTimeout)
	defer cancel()
	result, err := backend.Exec(shellCtx, cmd, fmt.Sprintf("DISPLAY=%s", display))
	if err != nil {
		log.Errorf("shell command failed: %v", err)
		return "", err
	}
	log.Debugf("shell command completed (%d bytes output)", len(result))
	return result, nil
}

func (j *Jail) containerSnapshot() (State, containerBackend, string) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.state, j.backend, j.display.String()
}

// screenshot returns a raw PNG of the Xvfb display via ImageMagick import.
func (j *Jail) screenshot(ctx context.Context) ([]byte, error) {
	state, backend, display := j.containerSnapshot()
	if state != StateRunning && state != StateStarting {
		log.Warningf("screenshot rejected: jail is %s", state)
		return nil, errNotRunning
	}
	if !backend.IsRunning() {
		log.Warningf("screenshot rejected: container is not ready")
		return nil, errContainerNotReady
	}

	log.Infof("capturing screenshot")
	screenshotCtx, cancel := context.WithTimeout(ctx, j.serverConfig.ExecTimeout)
	defer cancel()
	cmd := fmt.Sprintf("import -window root -display %s png:- 2>/dev/null", display)
	data, err := backend.ExecRaw(screenshotCtx, cmd)
	if err != nil {
		log.Errorf("screenshot failed: %v", err)
		return nil, err
	}
	log.Debugf("screenshot captured (%d bytes)", len(data))
	return data, nil
}

func (j *Jail) ScreenshotBase64(ctx context.Context) (string, error) {
	raw, err := j.screenshot(ctx)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	log.Debugf("screenshot encoded to base64 (%d bytes)", len(encoded))
	return encoded, nil
}

func (j *Jail) IsRunning() bool {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.state == StateRunning
}

func (j *Jail) CurrentState() State {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.state
}

// Collects Emacs log lines while waiting and fails fast if the container exits.
func (j *Jail) waitForSocket(ctx context.Context) error {
	socketPath := j.serverConfig.SocketPath()
	log.Infof("waiting for emacs-jail-rpc socket at %s", socketPath)

	containerExited := make(chan error, 1)
	go func() {
		containerExited <- j.backend.Wait(ctx)
	}()

	for {
		conn, err := net.DialTimeout("unix", socketPath, time.Second)
		if err == nil {
			_ = conn.Close()
			j.collectLogLines()
			j.collectStderrLines()
			log.Infof("emacs-jail-rpc socket is ready")
			return nil
		}

		select {
		case <-ctx.Done():
			j.collectLogLines()
			j.collectStderrLines()
			return fmt.Errorf("timed out waiting for socket %q: %w", socketPath, ctx.Err())
		case waitErr := <-containerExited:
			j.collectLogLines()
			j.collectStderrLines()
			if waitErr != nil && ctx.Err() != nil {
				return fmt.Errorf("timed out waiting for socket %q: %w", socketPath, ctx.Err())
			}
			return fmt.Errorf("emacs exited before opening socket %q", socketPath)
		case <-time.After(200 * time.Millisecond):
			j.collectLogLines()
			j.collectStderrLines()
		}
	}
}

func (j *Jail) collectLogLines() {
	all := j.backend.LogLines(0)
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(all) > len(j.logLines) {
		j.logLines = append(j.logLines, all[len(j.logLines):]...)
	}
}

func (j *Jail) collectStderrLines() {
	all := j.backend.StderrLines(0)
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(all) > len(j.stderrLines) {
		j.stderrLines = append(j.stderrLines, all[len(j.stderrLines):]...)
	}
}

// Returns an error if the jail is not running or the eval fails.
func (j *Jail) ReadBuffer(ctx context.Context, bufName string) (string, error) {
	log.Infof("reading buffer %q", bufName)
	expr := fmt.Sprintf(
		`(if (get-buffer %q)`+
			` (with-current-buffer %q (buffer-string))`+
			` "")`,
		bufName, bufName)
	result, err := j.EvalElisp(ctx, expr)
	if err != nil {
		log.Errorf("failed to read buffer %q: %v", bufName, err)
		return "", err
	}
	log.Infof("read buffer %q (%d bytes)", bufName, len(result))
	return result, nil
}

func (j *Jail) stopTailAndContainer() {
	j.mu.Lock()
	tail := j.stderrTail
	j.stderrTail = nil
	j.mu.Unlock()
	if tail != nil {
		tail.stop()
	}
	if j.backend.IsRunning() {
		_ = j.backend.Stop(context.Background())
	}
}
