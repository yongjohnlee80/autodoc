//go:build !gui

package tui

import "errors"

// nativeDiagram is a terminal build's: it draws no diagram natively.
func nativeDiagram(string) error { return errors.New("this build draws no diagram natively") }
