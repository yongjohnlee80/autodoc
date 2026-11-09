//go:build windows

package proctree

import (
	"testing"

	"golang.org/x/sys/windows"
)

var getConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// detachedFact is whether the helper has a console: none, when it was started detached.
func detachedFact() string {
	if h, _, _ := getConsoleWindow.Call(); h != 0 {
		return "console"
	}
	return "no console"
}

// detached is whether the helper has no console, none inherited and none made.
func detached(t *testing.T, fact string) bool {
	t.Helper()
	return fact == "no console"
}
