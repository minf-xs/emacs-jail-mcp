// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

//go:build e2e

package e2e_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	e2eTimeout   = 240 * time.Second
	startTimeout = 240 * time.Second
	evalTimeout  = 30 * time.Second
	// hangTimeout must exceed the slowest realistic Emacs init (which can take
	// ~30s on some machines). A shorter timeout would misfire on a slow-but-
	// legitimate init; only a genuine hang should trip it.
	hangTimeout = 60 * time.Second
)

// newMCPClient creates an SSE MCP client connected to the server via TCP and performs
// the MCP initialize handshake.
func newMCPClient(t *testing.T, srv *TestServer) *mcpclient.Client {
	t.Helper()

	baseURL := fmt.Sprintf("http://%s/sse", srv.Addr())

	c, err := mcpclient.NewSSEMCPClient(baseURL)
	require.NoError(t, err, "create SSE client")
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
	t.Cleanup(cancel)

	require.NoError(t, c.Start(ctx), "SSE client start")

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "e2e-test",
		Version: "0.0.1",
	}
	_, err = c.Initialize(ctx, initReq)
	require.NoError(t, err, "initialize")

	return c
}

func callTool(
	t *testing.T,
	c *mcpclient.Client,
	name string,
	args map[string]any,
) *mcp.CallToolResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), evalTimeout)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.CallTool(ctx, req)
	require.NoError(t, err, "CallTool %q", name)
	return result
}

func callToolLong(
	t *testing.T,
	c *mcpclient.Client,
	name string,
	args map[string]any,
	timeout time.Duration,
) *mcp.CallToolResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.CallTool(ctx, req)
	require.NoError(t, err, "CallTool %q", name)
	return result
}

func firstText(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func firstImageData(result *mcp.CallToolResult) string {
	for _, c := range result.Content {
		if ic, ok := c.(mcp.ImageContent); ok {
			return ic.Data
		}
	}
	return ""
}

type asyncToolResult struct {
	result *mcp.CallToolResult
	err    error
}

func callToolAsync(
	c *mcpclient.Client,
	name string,
	args map[string]any,
	timeout time.Duration,
) <-chan asyncToolResult {
	done := make(chan asyncToolResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		req := mcp.CallToolRequest{}
		req.Params.Name = name
		req.Params.Arguments = args

		result, err := c.CallTool(ctx, req)
		done <- asyncToolResult{result: result, err: err}
	}()
	return done
}

func waitToolResult(
	t *testing.T,
	done <-chan asyncToolResult,
	timeout time.Duration,
) asyncToolResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for async tool result")
		return asyncToolResult{}
	}
}

func startAsync(c *mcpclient.Client) <-chan asyncToolResult {
	return callToolAsync(c, "control", map[string]any{
		"action":     "start",
		"timeout_ms": hangTimeout.Milliseconds(),
	}, hangTimeout+30*time.Second)
}

func waitForStarting(t *testing.T, c *mcpclient.Client) {
	t.Helper()
	require.Eventually(t, func() bool {
		result := callTool(t, c, "control", map[string]any{"action": "status"})
		return strings.Contains(firstText(result), "starting")
	}, hangTimeout, 200*time.Millisecond)
}

func waitForFile(t *testing.T, c *mcpclient.Client, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		result := callTool(t, c, "shell", map[string]any{
			"command": "test -e " + path + " && echo exists",
		})
		return !result.IsError && strings.Contains(firstText(result), "exists")
	}, hangTimeout, 200*time.Millisecond)
}

func assertStartupProbesWork(t *testing.T, c *mcpclient.Client, shellMarker string) {
	t.Helper()

	status := callTool(t, c, "control", map[string]any{"action": "status"})
	assert.False(t, status.IsError)
	assert.Contains(t, firstText(status), "starting")

	require.Eventually(t, func() bool {
		shell := callTool(t, c, "shell", map[string]any{"command": "echo " + shellMarker})
		return !shell.IsError && strings.Contains(firstText(shell), shellMarker)
	}, hangTimeout, 200*time.Millisecond)

	require.Eventually(t, func() bool {
		screenshot := callTool(t, c, "screenshot", nil)
		return !screenshot.IsError && firstImageData(screenshot) != ""
	}, hangTimeout, 200*time.Millisecond)

	started := time.Now()
	eval := callTool(t, c, "eval", map[string]any{"expression": "(+ 1 2)"})
	assert.True(t, eval.IsError)
	assert.Contains(t, firstText(eval), "not ready")
	assert.Less(t, time.Since(started), 5*time.Second)
}

func assertRunningProbesWork(t *testing.T, c *mcpclient.Client, shellMarker string) {
	t.Helper()

	status := callTool(t, c, "control", map[string]any{"action": "status"})
	assert.False(t, status.IsError)
	assert.Contains(t, firstText(status), "running")

	shell := callTool(t, c, "shell", map[string]any{"command": "echo " + shellMarker})
	assert.False(t, shell.IsError)
	assert.Contains(t, firstText(shell), shellMarker)

	screenshot := callTool(t, c, "screenshot", nil)
	assert.False(t, screenshot.IsError)
	assert.NotEmpty(t, firstImageData(screenshot))

	eval := callTool(t, c, "eval", map[string]any{"expression": "(+ 4 5)"})
	assert.False(t, eval.IsError)
	assert.Contains(t, firstText(eval), "9")
}

func backtraceBlock(text string) string {
	bodyStart := 0
	start := strings.LastIndex(text, "\nbacktrace>\n")
	if start >= 0 {
		bodyStart = start + len("\nbacktrace>\n")
	} else if strings.HasPrefix(text, "backtrace>\n") {
		bodyStart = len("backtrace>\n")
	} else {
		return ""
	}

	end := strings.Index(text[bodyStart:], "\nbacktrace<")
	if end < 0 {
		return ""
	}
	return text[bodyStart : bodyStart+end]
}

// TestMCP is the main end-to-end test suite.
func TestMCP_FullCycle(t *testing.T) {
	var srv TestServer

	srv.Start(t)
	t.Cleanup(func() { srv.Stop(t) })

	c := newMCPClient(t, &srv)

	t.Run("ListTools", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		result, err := c.ListTools(ctx, mcp.ListToolsRequest{})
		require.NoError(t, err, "ListTools")

		want := []string{
			"control",
			"logs",
			"eval",
			"shell",
			"screenshot",
			"bytecomp",
		}
		assert.Len(t, result.Tools, len(want))
		nameSet := make(map[string]bool)
		for _, tool := range result.Tools {
			nameSet[tool.Name] = true
		}
		for _, name := range want {
			assert.True(t, nameSet[name], "tool %q not registered", name)
		}
	})

	t.Run("EvalBeforeStart", func(t *testing.T) {
		result := callTool(t, c, "eval",
			map[string]any{"expression": "(+ 1 2)"})
		assert.True(t, result.IsError,
			"expected error result, got success: %s",
			firstText(result))
		assert.Contains(t, firstText(result), "not running")
	})

	t.Run("Control/StatusBeforeStart", func(t *testing.T) {
		result := callTool(t, c, "control", map[string]any{"action": "status"})
		assert.False(t, result.IsError,
			"expected success, got error: %s",
			firstText(result))
		assert.Contains(t, firstText(result), "stopped")
	})

	t.Run("Control/Start", func(t *testing.T) {
		result := callToolLong(t, c, "control",
			map[string]any{"action": "start"},
			startTimeout,
		)

		require.False(t, result.IsError,
			"expected success, got error: %s", firstText(result))
		require.Contains(t, firstText(result), "started")
	})

	initLogsResult := callTool(t, c, "logs",
		map[string]any{"sources": "init_log"})
	initLogs := firstText(initLogsResult)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Emacs init log:\n%s", initLogs)
		}
	})

	t.Run("Logs", func(t *testing.T) {
		t.Run("InitLog", func(t *testing.T) {
			result := callTool(t, c, "logs",
				map[string]any{"sources": "init_log"})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			text := firstText(result)
			assert.NotContains(t, text, "(empty)")
			assert.Contains(t, text, "=== init_log ===")
		})

		t.Run("Stderr", func(t *testing.T) {
			result := callTool(t, c, "logs", map[string]any{"sources": "stderr"})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.Contains(t, firstText(result), "=== stderr ===")
		})

		t.Run("Buffers", func(t *testing.T) {
			marker := "e2e-logs-buffer-marker-99887"
			callTool(t, c, "eval",
				map[string]any{
					"expression": `(message "` +
						marker + `")`,
				})

			result := callTool(t, c, "logs",
				map[string]any{"sources": "messages"})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.Contains(t, firstText(result), marker)
		})

		t.Run("AllSources", func(t *testing.T) {
			result := callTool(t, c, "logs", map[string]any{
				"sources": "messages,warnings," +
					"backtrace,compile_log," +
					"async_compile_log," +
					"init_log,stderr",
			})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			text := firstText(result)
			for _, src := range []string{
				"messages", "warnings", "backtrace",
				"compile_log", "async_compile_log",
				"init_log", "stderr",
			} {
				assert.Contains(t, text, "=== "+src+" ===")
			}
		})
	})

	t.Run("Eval", func(t *testing.T) {
		t.Run("Simple", func(t *testing.T) {
			result := callTool(t, c, "eval",
				map[string]any{
					"expression": "(+ 1 2)",
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.Contains(t, firstText(result), "3")
		})

		t.Run("String", func(t *testing.T) {
			result := callTool(t, c, "eval",
				map[string]any{
					"expression": `(concat "hel" "lo")`,
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.Equal(t, "hello", firstText(result))
		})

		t.Run("Version", func(t *testing.T) {
			result := callTool(t, c, "eval",
				map[string]any{
					"expression": "emacs-version",
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			text := firstText(result)
			hasDigit := strings.IndexAny(text, "0123456789") >= 0
			hasDot := strings.Contains(text, ".")
			assert.True(t, hasDigit && hasDot,
				"emacs-version = %q, expected version string",
				text)
		})

		t.Run("ShellCommand", func(t *testing.T) {
			result := callTool(t, c, "eval",
				map[string]any{
					"expression": `(shell-command-to-string "echo hello")`,
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.Equal(t, "hello\n", firstText(result))
		})

		t.Run("Tty", func(t *testing.T) {
			result := callTool(t, c, "eval",
				map[string]any{
					"expression": `(shell-command-to-string (format "ps -o tty= -p %d" (emacs-pid)))`,
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			out := strings.TrimSpace(firstText(result))
			assert.NotEqual(t, "?", out, "emacs has no controlling tty")
			assert.NotEmpty(t, out, "emacs has no controlling tty")
		})
	})

	t.Run("Bytecomp", func(t *testing.T) {
		elFile := "/tmp/e2e-bytecomp-test.el"
		elContent := `;;; -*- lexical-binding: t -*-
(defun e2e-test-fn ()
  free-variable-ref)
(e2e-test-fn 42)
`
		writeCmd := `printf '%s' ` + `'` + elContent + `'` + ` > ` + elFile
		writeResult := callTool(t, c, "shell",
			map[string]any{"command": writeCmd})
		assert.False(t, writeResult.IsError,
			"expected success, got error: %s",
			firstText(writeResult))

		t.Run("NoFilter", func(t *testing.T) {
			result := callTool(t, c, "bytecomp",
				map[string]any{
					"file_path": elFile,
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			text := firstText(result)
			assert.Contains(t, text, `"file"`)
			assert.Contains(t, text, `"diagnostics"`)
			assert.Contains(t, text, `"summary"`)
			assert.NotContains(t, text, `"total":0`)
		})

		t.Run("FilterWarning", func(t *testing.T) {
			result := callTool(t, c, "bytecomp",
				map[string]any{
					"file_path": elFile,
					"severity":  "warning",
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.NotContains(t, firstText(result), `"severity":"error"`)
		})

		t.Run("FilterError", func(t *testing.T) {
			result := callTool(t, c, "bytecomp",
				map[string]any{
					"file_path": elFile,
					"severity":  "error",
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.NotContains(t, firstText(result), `"severity":"warning"`)
		})

		t.Run("NonexistentFile", func(t *testing.T) {
			result := callTool(t, c, "bytecomp",
				map[string]any{
					"file_path": "/tmp/" + "e2e-bytecomp-no-exist.el",
				})
			assert.True(t, result.IsError,
				"expected error result, got success: %s",
				firstText(result))
		})
	})

	t.Run("Shell", func(t *testing.T) {
		t.Run("Echo", func(t *testing.T) {
			result := callTool(t, c, "shell",
				map[string]any{
					"command": "echo hello",
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.Contains(t, firstText(result), "hello")
		})

		t.Run("Display", func(t *testing.T) {
			result := callTool(t, c, "shell",
				map[string]any{
					"command": "echo $DISPLAY",
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			assert.Contains(t, firstText(result), ":")
		})

		t.Run("Tty", func(t *testing.T) {
			result := callTool(t, c, "shell",
				map[string]any{
					"command": "tty",
				})
			assert.False(t, result.IsError,
				"expected success, got error: %s",
				firstText(result))
			out := strings.TrimSpace(firstText(result))
			assert.True(t, strings.HasPrefix(out, "/dev/"),
				"expected tty device path, got %q", out)
		})
	})

	t.Run("Screenshot", func(t *testing.T) {
		result := callTool(t, c, "screenshot", nil)
		assert.False(t, result.IsError,
			"expected success, got error: %s",
			firstText(result))

		data := firstImageData(result)
		require.NotEmpty(t, data, "no image data returned")

		decoded, err := base64.StdEncoding.DecodeString(data)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(decoded), 8,
			"data too short: %d bytes", len(decoded))

		const screenshotPNGMagic = "\x89PNG"
		assert.Equal(t, screenshotPNGMagic,
			string(decoded[:4]),
			"not a PNG: first 4 bytes = %x", decoded[:4])
	})

	t.Run("Control/StatusWhileRunning", func(t *testing.T) {
		result := callTool(t, c, "control",
			map[string]any{"action": "status"})
		assert.False(t, result.IsError,
			"expected success, got error: %s",
			firstText(result))
		assert.Contains(t, firstText(result), "running")
	})

	t.Run("Control/Restart", func(t *testing.T) {
		result := callToolLong(t, c, "control",
			map[string]any{"action": "restart"},
			startTimeout)
		assert.False(t, result.IsError,
			"expected success, got error: %s",
			firstText(result))
		assert.Contains(t, firstText(result), "restarted")
	})

	t.Run("Eval/AfterRestart", func(t *testing.T) {
		result := callTool(t, c, "eval",
			map[string]any{"expression": "(+ 2 3)"})
		assert.False(t, result.IsError,
			"expected success, got error: %s",
			firstText(result))
		assert.Contains(t, firstText(result), "5")
	})

	t.Run("Control/Stop", func(t *testing.T) {
		result := callTool(t, c, "control", map[string]any{"action": "stop"})
		assert.False(t, result.IsError,
			"expected success, got error: %s",
			firstText(result))
		assert.Contains(t, firstText(result), "stopped")
	})

	t.Run("EvalAfterStop", func(t *testing.T) {
		result := callTool(t, c, "eval",
			map[string]any{"expression": "(+ 1 2)"})
		assert.True(t, result.IsError,
			"expected error result, got success: %s",
			firstText(result))
		assert.Contains(t, firstText(result), "not running")
	})

	t.Run("Control/StatusAfterStop", func(t *testing.T) {
		result := callTool(t, c, "control",
			map[string]any{"action": "status"})
		assert.False(t, result.IsError,
			"expected success, got error: %s",
			firstText(result))
		assert.Contains(t, firstText(result), "stopped")
	})
}

func TestMCP_EmacsLauncherFails(t *testing.T) {
	var srv TestServer
	srv.WithLauncher(t, "echo emacs-jail launcher exits >&2\nexit 90")
	srv.Start(t)
	t.Cleanup(func() { srv.Stop(t) })

	c := newMCPClient(t, &srv)
	started := time.Now()
	result := callToolLong(t, c, "control", map[string]any{
		"action":     "start",
		"timeout_ms": hangTimeout.Milliseconds(),
	}, hangTimeout+30*time.Second)
	elapsed := time.Since(started)
	require.True(t, result.IsError, "expected startup fault error")
	assert.Contains(t, firstText(result), "exited before opening socket",
		"crash should be detected via container exit, not the hang timeout")
	assert.Less(t, elapsed, hangTimeout,
		"crash should fail fast via exit detection, not wait for the timeout")

	status := callTool(t, c, "control", map[string]any{"action": "status"})
	assert.False(t, status.IsError)
	assert.Contains(t, firstText(status), "stopped")
}

func TestMCP_EmacsLauncherHangs(t *testing.T) {
	t.Run("ToolsAndStop", func(t *testing.T) {
		var srv TestServer
		srv.WithLauncher(t, "echo emacs-jail launcher hang >&2\nwhile true; do sleep 1; done")
		srv.Start(t)
		t.Cleanup(func() { srv.Stop(t) })

		c := newMCPClient(t, &srv)
		startClient := newMCPClient(t, &srv)
		startDone := startAsync(startClient)
		waitForStarting(t, c)

		logs := callTool(t, c, "logs", map[string]any{"sources": "init_log,stderr"})
		assert.False(t, logs.IsError, "logs should work while starting: %s", firstText(logs))
		assert.Contains(t, firstText(logs), "=== init_log ===")
		assert.Contains(t, firstText(logs), "=== stderr ===")

		buffers := callTool(t, c, "logs", map[string]any{"sources": "messages"})
		assert.False(t, buffers.IsError)
		assert.Contains(t, firstText(buffers), "not ready")

		bytecomp := callTool(t, c, "bytecomp", map[string]any{"file_path": "/tmp/nope.el"})
		assert.True(t, bytecomp.IsError)
		assert.Contains(t, firstText(bytecomp), "not ready")

		assertStartupProbesWork(t, c, "launcher-hang-shell-ok")
		stop := callTool(t, c, "control", map[string]any{"action": "stop"})
		require.False(t, stop.IsError, "stop should cancel startup: %s", firstText(stop))
		assert.Contains(t, firstText(stop), "stopped")

		start := waitToolResult(t, startDone, 10*time.Second)
		require.NoError(t, start.err)
		require.NotNil(t, start.result)
		assert.True(t, start.result.IsError)
	})

	t.Run("Restart", func(t *testing.T) {
		var srv TestServer
		flag := "/tmp/emacs-jail-e2e-" + strings.NewReplacer("/", "-").Replace(t.Name())
		srv.WithLauncher(t, "flag="+flag+`
if [ ! -e "$flag" ]; then
  touch "$flag"
  echo emacs-jail launcher hang >&2
  while true; do sleep 1; done
fi
exec "$@"`)
		srv.Start(t)
		t.Cleanup(func() { srv.Stop(t) })

		c := newMCPClient(t, &srv)
		startClient := newMCPClient(t, &srv)
		startDone := startAsync(startClient)
		waitForStarting(t, c)
		waitForFile(t, c, flag)

		restart := callToolLong(t, c, "control", map[string]any{
			"action":         "restart",
			"swallow_errors": true,
			"timeout_ms":     hangTimeout.Milliseconds(),
		}, hangTimeout+60*time.Second)
		require.False(t, restart.IsError, "restart should recover: %s", firstText(restart))
		assert.Contains(t, firstText(restart), "restarted")

		start := waitToolResult(t, startDone, 10*time.Second)
		require.NoError(t, start.err)
		require.NotNil(t, start.result)
		assert.True(t, start.result.IsError)
		assertRunningProbesWork(t, c, "launcher-restart-shell-ok")
	})
}

func TestMCP_ElispInitFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		swallow bool
	}{
		{name: "Swallowed", swallow: true},
		{name: "Fatal", swallow: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var srv TestServer
			srv.WithPostInit(t, `(message "e2e post-init boom")
(error "e2e post-init boom")`)
			srv.Start(t)
			t.Cleanup(func() { srv.Stop(t) })

			c := newMCPClient(t, &srv)
			args := map[string]any{
				"action":     "start",
				"timeout_ms": hangTimeout.Milliseconds(),
			}
			if tc.swallow {
				args["swallow_errors"] = true
			}

			started := time.Now()
			result := callToolLong(t, c, "control", args, hangTimeout+30*time.Second)
			elapsed := time.Since(started)

			if tc.swallow {
				require.False(t, result.IsError,
					"swallowed init error should start: %s", firstText(result))
			} else {
				require.True(t, result.IsError, "fatal init error should fail")
				assert.Less(t, elapsed, hangTimeout,
					"fatal init error should fail fast, not wait for timeout")
				assert.Contains(t, firstText(result), "e2e post-init boom")
			}

			logs := callTool(t, c, "logs", map[string]any{
				"sources": "init_log,stderr",
				"limit":   int64(5000),
			})
			text := firstText(logs)
			assert.Contains(t, text, "e2e post-init boom")
			assert.Contains(t, text, "load!")
			if !tc.swallow {
				bt := backtraceBlock(text)
				require.NotEmpty(t, bt, "log should contain a backtrace block")
				assert.Contains(t, bt, "\n    ",
					"backtrace block should contain indented call frames")
			}

			if tc.swallow {
				assertRunningProbesWork(t, c, "swallowed-init-shell-ok")
				stop := callTool(t, c, "control", map[string]any{"action": "stop"})
				require.False(t, stop.IsError)
			}
		})
	}
}

func TestMCP_ElispInitHangs(t *testing.T) {
	t.Run("ToolsAndTimeout", func(t *testing.T) {
		var srv TestServer
		srv.WithPreInit(t, "(while t (sleep-for 1))")
		srv.Start(t)
		t.Cleanup(func() { srv.Stop(t) })

		c := newMCPClient(t, &srv)
		started := time.Now()
		result := callToolLong(t, c, "control", map[string]any{
			"action":     "start",
			"timeout_ms": hangTimeout.Milliseconds(),
		}, hangTimeout+30*time.Second)
		elapsed := time.Since(started)

		require.True(t, result.IsError, "start should fail when init hangs")
		assert.GreaterOrEqual(t, elapsed, hangTimeout,
			"a genuine hang should only be reported after the full timeout elapses")
		assert.Contains(t, firstText(result), "timed out waiting for socket")
	})

	t.Run("ToolsAndStop", func(t *testing.T) {
		var srv TestServer
		srv.WithPreInit(t, "(while t (sleep-for 1))")
		srv.Start(t)
		t.Cleanup(func() { srv.Stop(t) })

		c := newMCPClient(t, &srv)
		startClient := newMCPClient(t, &srv)
		startDone := startAsync(startClient)
		waitForStarting(t, c)
		assertStartupProbesWork(t, c, "init-hang-shell-ok")

		stop := callTool(t, c, "control", map[string]any{"action": "stop"})
		require.False(t, stop.IsError, "stop should cancel startup: %s", firstText(stop))
		assert.Contains(t, firstText(stop), "stopped")

		start := waitToolResult(t, startDone, 10*time.Second)
		require.NoError(t, start.err)
		require.NotNil(t, start.result)
		assert.True(t, start.result.IsError)
	})

	t.Run("Restart", func(t *testing.T) {
		var srv TestServer
		flag := "/tmp/emacs-jail-e2e-" + strings.NewReplacer("/", "-").Replace(t.Name())
		srv.WithPreInit(t, `(unless (file-exists-p "`+flag+`")
  (write-region "" nil "`+flag+`")
  (while t (sleep-for 1)))`)
		srv.Start(t)
		t.Cleanup(func() { srv.Stop(t) })

		c := newMCPClient(t, &srv)
		startClient := newMCPClient(t, &srv)
		startDone := startAsync(startClient)
		waitForStarting(t, c)
		waitForFile(t, c, flag)

		restart := callToolLong(t, c, "control", map[string]any{
			"action":         "restart",
			"swallow_errors": true,
			"timeout_ms":     hangTimeout.Milliseconds(),
		}, hangTimeout+60*time.Second)
		require.False(t, restart.IsError, "restart should recover: %s", firstText(restart))
		assert.Contains(t, firstText(restart), "restarted")

		start := waitToolResult(t, startDone, 10*time.Second)
		require.NoError(t, start.err)
		require.NotNil(t, start.result)
		assert.True(t, start.result.IsError)
		assertRunningProbesWork(t, c, "init-restart-shell-ok")
	})
}
