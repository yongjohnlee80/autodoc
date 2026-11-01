//go:build !unix

package tui

import "errors"

// privateDir: the editor socket is a unix socket in a private folder, which this system has not.
func privateDir(string) error { return errors.New("the editor socket needs a unix system") }

// sweepEditorSockets: no editor socket here, so none is left to sweep.
func sweepEditorSockets(string) {}
