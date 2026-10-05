//go:build !race

package tui

// raceFactor is 1 outside a -race build (race_on_test.go).
const raceFactor = 1
