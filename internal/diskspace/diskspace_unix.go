//go:build linux || darwin

package diskspace

import "syscall"

// free is statfs's blocks available to an unprivileged writer, in bytes.
func free(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
