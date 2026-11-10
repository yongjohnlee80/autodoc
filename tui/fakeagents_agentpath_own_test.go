package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Copies of the agents' test fakes agentpath_own_test.go declares, for the host's cells.

// pathOf is the PATH an agent's start sets (its argv's PATH=), "" for none.
func pathOf(args []string) string {
	path := ""
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "PATH="); ok {
			path = v // the last one is what the agent gets
		}
	}
	return path
}

// fakeBuild makes an empty autodoc build in a new folder dir, named as this OS names a program
// (autodoc.exe on Windows), and answers its path: what an agent's PATH leads to.
func fakeBuild(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "autodoc"
	if filepath.Separator == '\\' {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
