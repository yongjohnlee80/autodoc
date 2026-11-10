//go:build windows

package procenv

import "path/filepath"

// launcher is no file: the build itself is on the agent's PATH (pathEntry), and a script or batch
// file Windows runs can't carry its caller's arguments as themselves.
func launcher(string, []string) ([]LauncherFile, error) { return nil, nil }

// pathEntry is program's own folder: the build, found as autodoc(.exe) through PATHEXT, its config
// given by $AUTODOC_CONFIG. No link: a symbolic link needs Developer Mode or an administrator.
func pathEntry(_, _, program string) string { return filepath.Dir(program) }

// makePathEntry has nothing to make.
func makePathEntry(_, _ string) error { return nil }
