// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gavv/emacs-jail-mcp/internal/cli"
	"github.com/gavv/emacs-jail-mcp/internal/config"
)

// TestServer manages the lifecycle of an in-process MCP server for e2e tests.
type TestServer struct {
	cancel       context.CancelFunc
	errCh        <-chan error
	launcherPath string
	preInitPath  string
	postInitPath string
}

func (s *TestServer) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", config.E2ETestMCPPort)
}

func (s *TestServer) Port() string {
	return fmt.Sprintf("%d", config.E2ETestMCPPort)
}

func (s *TestServer) WithLauncher(t *testing.T, body string) {
	t.Helper()
	require.Empty(t, s.errCh, "WithLauncher must be called before Start")
	path := filepath.Join(t.TempDir(), "emacs-launcher")
	script := "#!/bin/sh\n" + body + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	s.launcherPath = path
}

func (s *TestServer) WithPreInit(t *testing.T, body string) {
	t.Helper()
	require.Empty(t, s.errCh, "WithPreInit must be called before Start")
	path := filepath.Join(t.TempDir(), "pre-init.el")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	s.preInitPath = path
}

func (s *TestServer) WithPostInit(t *testing.T, body string) {
	t.Helper()
	require.Empty(t, s.errCh, "WithPostInit must be called before Start")
	path := filepath.Join(t.TempDir(), "post-init.el")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	s.postInitPath = path
}

func (s *TestServer) Start(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	errCh := make(chan error, 1)
	s.errCh = errCh
	go func() {
		cmd := cli.NewRootCmd()
		cmd.SetContext(ctx)
		args := []string{
			"serve",
			"--mcp-port", fmt.Sprintf("%d", config.E2ETestMCPPort),
		}
		if s.launcherPath != "" {
			args = append(args, "--emacs-launcher", s.launcherPath)
		}
		if s.preInitPath != "" {
			args = append(args, "--emacs-pre-init", s.preInitPath)
		}
		if s.postInitPath != "" {
			args = append(args, "--emacs-post-init", s.postInitPath)
		}
		cmd.SetArgs(args)
		errCh <- cmd.Execute()
	}()

	waitForTCP(t, s.Addr(), 10*time.Second)
}

func waitForTCP(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, time.Second)
		if dialErr == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server at %s not ready after %s", addr, timeout)
}

func (s *TestServer) Stop(t *testing.T) {
	t.Helper()

	if s.cancel != nil {
		s.cancel()
	}

	select {
	case err := <-s.errCh:
		assert.NoError(t, err, "server exited with error")
	case <-time.After(60 * time.Second):
		t.Error("server did not exit within 60s")
	}
}
