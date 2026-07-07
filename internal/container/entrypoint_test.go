// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package container_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/gavv/emacs-jail-mcp/internal/config"
	"github.com/gavv/emacs-jail-mcp/internal/container"
	"github.com/gavv/emacs-jail-mcp/internal/display"
)

func TestEntrypointEnvIncludesRuntimeConfig(t *testing.T) {
	cfg := config.DefaultServer()
	cfg.EmacsLauncher = "/tmp/launcher"
	cfg.EmacsPreInitFile = "/tmp/pre.el"
	cfg.EmacsPostInitFile = "/tmp/post.el"
	cfg.SwallowErrors = true
	env := container.EntrypointEnv(cfg, display.Default(20))
	envText := strings.Join(env, "\n")

	assert.Contains(t, envText, "EMACS_JAIL_LOCK_PATH=")
	assert.Contains(t, envText, "EMACS_JAIL_SOCKET_PATH=")
	assert.Contains(t, envText, "EMACS_JAIL_EMACS_LAUNCHER=/tmp/launcher")
	assert.Contains(t, envText, "EMACS_JAIL_PRE_INIT_FILE=/tmp/pre.el")
	assert.Contains(t, envText, "EMACS_JAIL_POST_INIT_FILE=/tmp/post.el")
	assert.Contains(t, envText, "EMACS_JAIL_SWALLOW_ERRORS=true")
}
