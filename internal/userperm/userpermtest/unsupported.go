//go:build !(linux || darwin || windows)

package userpermtest

import "testing"

// Expose has no form on this OS, which userperm doesn't implement (userperm's unsupported.go).
func Expose(t testing.TB, _ string) { t.Skip("userperm has no form of an exposed file on this OS") }

// ExposeNew has no form on this OS.
func ExposeNew(t testing.TB, _ string) {
	t.Skip("userperm has no form of an exposed folder on this OS")
}
