//go:build linux || darwin

package fileinuse

// Is is false: a file another program holds open refuses nothing here.
func Is(error) bool { return false }
