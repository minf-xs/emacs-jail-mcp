// Copyright (c) Victor Gaydov and contributors
// Licensed under GPLv3+

package config

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"os"
)

// DefaultEmacsBinary returns the emacs binary to use. It prefers "emacs" if
// found in PATH, otherwise scans PATH for "emacs-<ver>" executables and
// returns the one with the highest version number.
//
// On NixOS the emacs binary is a symlink into /nix/store. In that case the
// canonical absolute store path is returned so the container can exec the
// host binary directly via the /nix/store volume mount. Otherwise the plain
// binary name is returned and the container uses its own emacs.
func DefaultEmacsBinary() string {
	if _, err := exec.LookPath("emacs"); err == nil {
		return ResolveEmacsBinary("emacs")
	}

	best := "emacs"
	var bestVersion []int
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "emacs-") {
				continue
			}
			version, ok := parseEmacsVersion(name[len("emacs-"):])
			if !ok || !versionGreater(version, bestVersion) {
				continue
			}
			if _, err := exec.LookPath(name); err == nil {
				best = name
				bestVersion = version
			}
		}
	}
	if best != "emacs" {
		return ResolveEmacsBinary(best)
	}
	return best
}

// ResolveEmacsBinary maps an emacs binary name or path to the absolute
// canonical path when it points into /nix/store, otherwise returns it
// unchanged.
//
// NixOS installs emacs as a symlink chain (profiles -> /nix/store). The
// resolved store path is visible inside the container through the /nix/store
// volume mount, so exec'ing it runs the host emacs. Plain names like "emacs"
// are left alone so the container falls back to its own emacs.
func ResolveEmacsBinary(binary string) string {
	if binary == "" {
		return binary
	}
	candidate := binary
	if !strings.Contains(candidate, "/") {
		path, err := exec.LookPath(candidate)
		if err != nil {
			return binary
		}
		candidate = path
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return binary
	}
	if resolved == "/nix/store" || strings.HasPrefix(resolved, "/nix/store/") {
		return resolved
	}
	return binary
}

func parseEmacsVersion(s string) ([]int, bool) {
	parts := strings.Split(s, ".")
	if len(parts) == 0 {
		return nil, false
	}
	version := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		version = append(version, n)
	}
	return version, true
}

func versionGreater(a, b []int) bool {
	if len(b) == 0 {
		return true
	}
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	for i := 0; i < maxLen; i++ {
		var av, bv int
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}
