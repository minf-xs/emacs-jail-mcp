// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gavv/emacs-jail-mcp/internal/jail"
)

func controlTool() mcp.Tool {
	return mcp.NewTool("control",
		mcp.WithDescription(
			"Control the Emacs jail lifecycle. "+
				"Use action=start to launch the Podman container "+
				"with Xvfb and Emacs (must be called before other tools). "+
				"Use action=stop to shut down Emacs and remove the container. "+
				"Use action=restart to stop and restart the jail (clears logs). "+
				"Use action=status to check the current jail state."),
		mcp.WithString("action",
			mcp.Required(),
			mcp.Description("The lifecycle action to perform: start, stop, restart, or status."),
			mcp.Enum("start", "stop", "restart", "status"),
		),
		mcp.WithNumber("timeout_ms",
			mcp.Description("Optional timeout in milliseconds for start and restart actions."),
		),
		mcp.WithBoolean("swallow_errors",
			mcp.Description("Swallow Emacs init errors and continue startup."),
		),
	)
}

func controlHandler(sb *jail.Jail) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		action := mcp.ParseString(req, "action", "")

		timeout := timeoutFromRequest(req)
		swallowErrors := mcp.ParseBoolean(req, "swallow_errors", false)
		switch action {
		case "start":
			return handleStart(ctx, sb, timeout, swallowErrors)
		case "stop":
			return handleStop(ctx, sb)
		case "restart":
			return handleRestart(ctx, sb, timeout, swallowErrors)
		case "status":
			return handleStatus(sb)
		default:
			return mcp.NewToolResultError(
				fmt.Sprintf("unknown action %q; valid actions: start, stop, restart, status", action),
			), nil
		}
	}
}

func timeoutFromRequest(req mcp.CallToolRequest) time.Duration {
	timeoutMS := mcp.ParseInt64(req, "timeout_ms", 0)
	if timeoutMS <= 0 {
		return 0
	}
	return time.Duration(timeoutMS) * time.Millisecond
}

func handleStart(
	ctx context.Context,
	sb *jail.Jail,
	timeout time.Duration,
	swallowErrors bool,
) (*mcp.CallToolResult, error) {
	if err := sb.Start(ctx, timeout, swallowErrors); err != nil {
		msg := err.Error()
		if lines := sb.LogLines(); len(lines) > 0 {
			tail := lines
			if len(tail) > 20 {
				tail = tail[len(tail)-20:]
			}
			msg = fmt.Sprintf("%s\n\nEmacs log (last %d lines):\n%s",
				msg, len(tail), strings.Join(tail, "\n"))
		}
		return mcp.NewToolResultError(msg), nil
	}

	msg := "Jail started successfully."
	if lines := sb.LogLines(); len(lines) > 0 {
		tail := lines
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
		msg = fmt.Sprintf("%s\n\nEmacs log (last %d lines):\n%s",
			msg, len(tail), strings.Join(tail, "\n"))
	}
	return mcp.NewToolResultText(msg), nil
}

func handleStop(ctx context.Context, sb *jail.Jail) (*mcp.CallToolResult, error) {
	if err := sb.Stop(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText("Jail stopped successfully."), nil
}

func handleRestart(
	ctx context.Context,
	sb *jail.Jail,
	timeout time.Duration,
	swallowErrors bool,
) (*mcp.CallToolResult, error) {
	if err := sb.Restart(ctx, timeout, swallowErrors); err != nil {
		msg := err.Error()
		if lines := sb.LogLines(); len(lines) > 0 {
			tail := lines
			if len(tail) > 20 {
				tail = tail[len(tail)-20:]
			}
			msg = fmt.Sprintf("%s\n\nEmacs log (last %d lines):\n%s",
				msg, len(tail), strings.Join(tail, "\n"))
		}
		return mcp.NewToolResultError(msg), nil
	}

	msg := "Jail restarted successfully."
	if lines := sb.LogLines(); len(lines) > 0 {
		tail := lines
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
		msg = fmt.Sprintf("%s\n\nEmacs log (last %d lines):\n%s",
			msg, len(tail), strings.Join(tail, "\n"))
	}
	return mcp.NewToolResultText(msg), nil
}

func handleStatus(sb *jail.Jail) (*mcp.CallToolResult, error) {
	state := sb.CurrentState().String()
	return mcp.NewToolResultText(fmt.Sprintf("Jail state: %s", state)), nil
}
