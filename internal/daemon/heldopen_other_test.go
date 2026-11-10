//go:build !windows

package daemon

// heldOpen is Windows' refusal of a change to something another handle holds (heldopen_windows_test.go):
// no other OS refuses it.
func heldOpen(error) bool { return false }
