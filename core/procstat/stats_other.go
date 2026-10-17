//go:build !linux && !darwin

package procstat

// readOS is every other platform's: nothing beyond Go's own sizes.
func readOS(*Stats) {}
