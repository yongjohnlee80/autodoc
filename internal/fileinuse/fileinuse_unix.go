//go:build linux || darwin

package fileinuse

// Is is false: a file another program holds open refuses nothing here.
func Is(error) bool { return false }

// ReplaceRefused is false: a rename over a file another program holds open succeeds here, and a
// refusal is the folder's, said as the caller says any error.
func ReplaceRefused(error) bool { return false }
