package main

import "github.com/yongjohnlee80/golib/tui/term"

// termOptions folds Option+letter on a Mac: a terminal whose Option key does not send Meta types
// "ƒ" for Option+F, and the menu's Alt+letter would never fire. It costs typing those characters;
// a paste still carries them.
func termOptions() []term.Option { return []term.Option{term.WithOptionFold()} }
