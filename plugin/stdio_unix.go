//go:build unix

package plugin

import (
	"errors"
	"net"
	"os"
	"syscall"
)

// stdio is the plugin's end of the link: its stdin and stdout, made pollable (non-blocking, then
// re-opened, so the runtime's poller takes them), since the link's writes carry deadlines. After it,
// os.Stdout is os.Stderr: stdout is the protocol's, and a stray print would break it.
func stdio() (net.Conn, error) {
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		return nil, errors.New("plugin: stdin is a terminal; a plugin runs under AutoDoc, from its Plugins menu")
	}
	for _, fd := range []int{0, 1} {
		if err := syscall.SetNonblock(fd, true); err != nil {
			return nil, err
		}
	}
	r, w := os.NewFile(0, "stdin"), os.NewFile(1, "stdout")
	os.Stdout = os.Stderr
	return FileConn(r, w), nil
}
