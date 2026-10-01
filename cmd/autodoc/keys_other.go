//go:build !darwin

package main

import "github.com/yongjohnlee80/golib/tui/term"

// termOptions: elsewhere a terminal's Alt arrives as Alt.
func termOptions() []term.Option { return nil }
