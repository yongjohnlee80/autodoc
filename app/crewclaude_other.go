//go:build !linux

package app

import "errors"

// parentOf has no reader here: Claude's waker binds through Linux's /proc.
func parentOf(int) (int, error) { return 0, errors.New("no process tree reader on this platform") }
