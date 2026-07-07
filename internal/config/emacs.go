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
func DefaultEmacsBinary() string {
	if _, err := exec.LookPath("emacs"); err == nil {
		return "emacs"
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
	return best
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
