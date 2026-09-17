// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gavv/emacs-jail-mcp/internal/config"
)

func TestResolveEmacsBinaryEmpty(t *testing.T) {
	assert.Equal(t, "", config.ResolveEmacsBinary(""))
}

func TestResolveEmacsBinaryUnknownName(t *testing.T) {
	name := "nonexistent-emacs-binary-xyz"
	assert.Equal(t, name, config.ResolveEmacsBinary(name))
}

func TestResolveEmacsBinaryMissingPath(t *testing.T) {
	path := "/nonexistent-dir-xyz/emacs"
	assert.Equal(t, path, config.ResolveEmacsBinary(path))
}

func TestResolveEmacsBinaryNonNixSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "emacs-real")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o755))
	link := filepath.Join(dir, "emacs-link")
	require.NoError(t, os.Symlink(target, link))

	// Symlink resolves outside /nix/store, so the input is kept as-is
	// and the container falls back to its own emacs.
	assert.Equal(t, link, config.ResolveEmacsBinary(link))
}

func TestResolveEmacsBinaryBareName(t *testing.T) {
	// A bare name resolving outside /nix/store is left untouched.
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-emacs-xyz")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	assert.Equal(t, "fake-emacs-xyz", config.ResolveEmacsBinary("fake-emacs-xyz"))
}

func TestDefaultEmacsBinaryNonEmpty(t *testing.T) {
	assert.NotEmpty(t, config.DefaultEmacsBinary())
}
