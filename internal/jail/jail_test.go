// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package jail

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gavv/emacs-jail-mcp/internal/config"
	"github.com/gavv/emacs-jail-mcp/internal/display"
)

// mockBackend is a test implementation of containerBackend.
type mockBackend struct {
	mu          sync.Mutex
	StartErr    error
	StopErr     error
	ExecOut     string
	ExecErr     error
	ExecRawOut  []byte
	ExecRawErr  error
	Running     bool
	Lines       []string
	StderrOut   []string
	StartBlock  chan struct{}
	StartCalled chan struct{}
	StopCalls   int
}

func (m *mockBackend) Start(ctx context.Context) error {
	if m.StartCalled != nil {
		close(m.StartCalled)
		m.StartCalled = nil
	}
	if m.StartBlock != nil {
		select {
		case <-m.StartBlock:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if m.StartErr != nil {
		return m.StartErr
	}
	m.mu.Lock()
	m.Running = true
	m.mu.Unlock()
	return nil
}

func (m *mockBackend) Stop(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StopCalls++
	if m.StopErr != nil {
		return m.StopErr
	}
	m.Running = false
	return nil
}

func (m *mockBackend) Wait(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (m *mockBackend) Exec(_ context.Context, _ string, _ ...string) (string, error) {
	return m.ExecOut, m.ExecErr
}

func (m *mockBackend) ExecRaw(_ context.Context, _ string) ([]byte, error) {
	return m.ExecRawOut, m.ExecRawErr
}

func (m *mockBackend) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Running
}

func (m *mockBackend) LogLines(_ int) []string {
	return m.Lines
}

func (m *mockBackend) StderrLines(_ int) []string {
	return m.StderrOut
}

// mockClient is a test implementation of emacsClient.
type mockClient struct {
	ConnectErr   error
	EvalOut      string
	EvalErr      error
	CloseErr     error
	ConnectCalls int
	CloseCalls   int
}

func (m *mockClient) Connect(_ context.Context) error {
	m.ConnectCalls++
	return m.ConnectErr
}

func (m *mockClient) EvalElisp(_ context.Context, _ string) (string, error) {
	return m.EvalOut, m.EvalErr
}

func (m *mockClient) Close() error {
	m.CloseCalls++
	return m.CloseErr
}

// newMockJail creates a Jail backed by mock backend and client.
func newMockJail(cfg *config.ServerConfig) (*Jail, *mockBackend, *mockClient) {
	if cfg == nil {
		cfg = config.DefaultServer()
	}
	b := &mockBackend{}
	c := &mockClient{}
	jl := &Jail{
		serverConfig: cfg,
		display:      display.Default(20),
		backend:      b,
		client:       c,
		state:        StateStopped,
	}
	return jl, b, c
}

// newTestJail creates a jail with default config for tests.
func newTestJail() *Jail {
	cfg := config.DefaultServer()
	return New(cfg)
}

func TestInitialState(t *testing.T) {
	jl := newTestJail()
	assert.False(t, jl.IsRunning())
	assert.Equal(t, StateStopped, jl.CurrentState())
}

func TestStopWhenStopped(t *testing.T) {
	jl := newTestJail()
	err := jl.Stop(context.Background())
	assert.Error(t, err)
}

func TestEvalWhenStopped(t *testing.T) {
	jl := newTestJail()
	_, err := jl.EvalElisp(context.Background(), "(+ 1 2)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestShellWhenStopped(t *testing.T) {
	jl := newTestJail()
	_, err := jl.Shell(context.Background(), "echo hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestScreenshotBase64WhenStopped(t *testing.T) {
	jl := newTestJail()
	_, err := jl.ScreenshotBase64(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

// TestStartFailsWhenContainerUnavailable verifies Start transitions back to
// Stopped when the container cannot be launched.
func TestStartFailsWhenContainerUnavailable(t *testing.T) {
	cfg := config.DefaultServer()
	cfg.PodmanBinary = "/nonexistent/podman"
	cfg.UseSudo = false
	cfg.StartTimeout = 1 * time.Second

	jl := New(cfg)
	startErr := jl.Start(context.Background(), 0, false)

	assert.Error(t, startErr)
	assert.Equal(t, StateStopped, jl.CurrentState())
	assert.False(t, jl.IsRunning())
}

func TestStartWhileRunningSucceeds(t *testing.T) {
	jl := newTestJail()
	jl.state = StateRunning

	err := jl.Start(context.Background(), 0, false)
	assert.NoError(t, err)
	assert.Equal(t, StateRunning, jl.CurrentState())
}

// listenUnix creates a Unix socket listener at the given path and returns it.
// The caller is responsible for closing the listener and removing the file.
func listenUnix(t *testing.T, path string) net.Listener {
	t.Helper()
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	require.NoError(t, err, "listen unix %q", path)
	return l
}

// newMockCfg returns a ServerConfig with a temp-dir socket dir, suitable for mock tests.
func newMockCfg(t *testing.T) *config.ServerConfig {
	t.Helper()
	cfg := config.DefaultServer()
	cfg.EmacsSocketDir = t.TempDir()
	cfg.ContainerPrefix = "mock"
	cfg.StartTimeout = 2 * time.Second
	cfg.StopTimeout = 2 * time.Second
	return cfg
}

func TestMockStartStop(t *testing.T) {
	cfg := newMockCfg(t)
	jl, backend, _ := newMockJail(cfg)

	l := listenUnix(t, cfg.SocketPath())
	defer func() {
		_ = l.Close()
		_ = os.Remove(cfg.SocketPath())
	}()
	go func() { _, _ = l.Accept() }()

	require.NoError(t, jl.Start(context.Background(), 0, false))

	assert.True(t, jl.IsRunning())
	assert.Equal(t, StateRunning, jl.CurrentState())
	assert.True(t, backend.Running)
	assert.NotNil(t, jl.client)

	require.NoError(t, jl.Stop(context.Background()))

	assert.False(t, jl.IsRunning())
	assert.Equal(t, StateStopped, jl.CurrentState())
}

func TestMockStartBackendFailure(t *testing.T) {
	cfg := newMockCfg(t)
	jl, backend, _ := newMockJail(cfg)
	backend.StartErr = fmt.Errorf("jail is not running")

	err := jl.Start(context.Background(), 0, false)
	require.Error(t, err)

	assert.Equal(t, StateStopped, jl.CurrentState())
	assert.False(t, jl.IsRunning())
}

func TestStartingStateDoesNotBlockStatusOrLogs(t *testing.T) {
	cfg := newMockCfg(t)
	jl, backend, _ := newMockJail(cfg)
	backend.StartBlock = make(chan struct{})
	backend.StartCalled = make(chan struct{})
	backend.Lines = []string{"init line"}
	backend.StderrOut = []string{"stderr line"}

	startDone := make(chan error, 1)
	go func() { startDone <- jl.Start(context.Background(), 0, false) }()
	<-backend.StartCalled

	assert.Eventually(t, func() bool {
		return jl.CurrentState() == StateStarting
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"init line"}, jl.LogLines())
	assert.Equal(t, []string{"stderr line"}, jl.StderrLines())

	require.NoError(t, jl.Stop(context.Background()))
	assert.Equal(t, StateStopped, jl.CurrentState())
	require.Error(t, <-startDone)
}

func TestConcurrentStartWaitsForOriginalStart(t *testing.T) {
	cfg := newMockCfg(t)
	jl, backend, _ := newMockJail(cfg)
	backend.StartBlock = make(chan struct{})
	backend.StartCalled = make(chan struct{})

	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- jl.Start(context.Background(), 0, false) }()
	<-backend.StartCalled
	go func() { secondDone <- jl.Start(context.Background(), 0, false) }()

	l := listenUnix(t, cfg.SocketPath())
	defer func() {
		_ = l.Close()
		_ = os.Remove(cfg.SocketPath())
	}()
	go func() { _, _ = l.Accept() }()
	close(backend.StartBlock)

	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	assert.Equal(t, StateRunning, jl.CurrentState())
}

func TestStopCancelsBlockedStart(t *testing.T) {
	cfg := newMockCfg(t)
	jl, backend, _ := newMockJail(cfg)
	backend.StartBlock = make(chan struct{})
	backend.StartCalled = make(chan struct{})

	startDone := make(chan error, 1)
	go func() { startDone <- jl.Start(context.Background(), 0, false) }()
	<-backend.StartCalled

	require.NoError(t, jl.Stop(context.Background()))
	require.Error(t, <-startDone)
	assert.Equal(t, StateStopped, jl.CurrentState())
	assert.Equal(t, 1, backend.StopCalls)
}

func TestStartingEvalFailsFast(t *testing.T) {
	jl := newTestJail()
	jl.state = StateStarting

	_, err := jl.EvalElisp(context.Background(), "(+ 1 2)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready")
}

func TestStartingShellUsesRunningContainer(t *testing.T) {
	jl, backend, _ := newMockJail(nil)
	backend.ExecOut = "hello\n"
	backend.Running = true
	jl.state = StateStarting

	out, err := jl.Shell(context.Background(), "echo hello")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", out)
}

func TestStartingScreenshotUsesRunningContainer(t *testing.T) {
	jl, backend, _ := newMockJail(nil)
	backend.ExecRawOut = []byte("PNG")
	backend.Running = true
	jl.state = StateStarting

	result, err := jl.ScreenshotBase64(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "UE5H", result)
}

func TestMockEvalDelegates(t *testing.T) {
	jl, _, client := newMockJail(nil)
	client.EvalOut = "42"
	jl.state = StateRunning

	result, err := jl.EvalElisp(context.Background(), "(+ 1 2)")
	require.NoError(t, err)
	assert.Equal(t, "42", result)
}

func TestMockEvalError(t *testing.T) {
	jl, _, client := newMockJail(nil)
	client.EvalErr = fmt.Errorf("jail is not running")
	jl.state = StateRunning

	_, err := jl.EvalElisp(context.Background(), "(foo)")
	assert.Error(t, err)
}

func TestMockShellDelegates(t *testing.T) {
	jl, backend, _ := newMockJail(nil)
	backend.ExecOut = "hello\n"
	backend.Running = true
	jl.state = StateRunning

	out, err := jl.Shell(context.Background(), "echo hello")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", out)
}

func TestMockScreenshotBase64Delegates(t *testing.T) {
	jl, backend, _ := newMockJail(nil)
	backend.ExecRawOut = []byte("PNG")
	backend.Running = true
	jl.state = StateRunning

	result, err := jl.ScreenshotBase64(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	// base64("PNG") = "UE5H"
	assert.Equal(t, "UE5H", result)
}

func TestReadBufferWhenNotRunning(t *testing.T) {
	jl := newTestJail()
	_, err := jl.ReadBuffer(context.Background(), "*Messages*")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestReadBufferDelegates(t *testing.T) {
	jl, _, client := newMockJail(nil)
	client.EvalOut = "hello from buffer"
	jl.state = StateRunning

	result, err := jl.ReadBuffer(context.Background(), "*Messages*")
	require.NoError(t, err)
	assert.Equal(t, "hello from buffer", result)
}

func TestRestartWhenStopped(t *testing.T) {
	jl := newTestJail()
	err := jl.Restart(context.Background(), 0, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestRestartStopFails(t *testing.T) {
	jl, _, _ := newMockJail(nil)
	jl.state = StateRunning
	failing := &mockBackend{StopErr: fmt.Errorf("kill failed"), Running: true}
	jl.backend = failing

	err := jl.Restart(context.Background(), 0, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop failed")
}

func TestRestartStartFails(t *testing.T) {
	cfg := newMockCfg(t)
	jl, backend, _ := newMockJail(cfg)
	// Set jail directly to Running state (skip actual Start).
	jl.state = StateRunning
	jl.logLines = []string{"old log line"}
	backend.Running = true
	// Backend stop succeeds, but second start (after stop) will fail.
	backend.StartErr = fmt.Errorf("container launch error")

	err := jl.Restart(context.Background(), 0, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start failed")
	assert.Equal(t, StateStopped, jl.CurrentState())
	// Logs must have been cleared even though start failed.
	assert.Empty(t, jl.LogLines())
}

func TestMockRestart(t *testing.T) {
	cfg := newMockCfg(t)
	jl, backend, _ := newMockJail(cfg)

	l := listenUnix(t, cfg.SocketPath())
	defer func() {
		_ = l.Close()
		_ = os.Remove(cfg.SocketPath())
	}()
	go func() {
		for {
			_, _ = l.Accept()
		}
	}()

	// Start to get into Running state.
	require.NoError(t, jl.Start(context.Background(), 0, false))

	// Plant some log lines to verify they are cleared on Restart.
	jl.logLines = []string{"old line 1", "old line 2"}

	require.NoError(t, jl.Restart(context.Background(), 0, false))

	assert.True(t, jl.IsRunning())
	assert.Equal(t, StateRunning, jl.CurrentState())
	// Backend must have been cycled: stopped then started again.
	assert.True(t, backend.IsRunning())
	// Old log lines must be gone.
	assert.Empty(t, jl.LogLines(), "old log lines should be cleared after Restart")
}
