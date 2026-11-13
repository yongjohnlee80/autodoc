//go:build !unix

package plugin

import (
	"net"
	"os"
)

// stdio is the plugin's end of the link, over its stdin and stdout, where they aren't Unix
// descriptors to duplicate (stdio_unix.go). Windows' anonymous pipes have no deadlines of their own,
// and FileConn gives the link its deadlines over them. After it, os.Stdout is os.Stderr: stdout is
// the protocol's, and a stray print would break it.
func stdio() (net.Conn, error) {
	if err := refuseTerminal(os.Stdin); err != nil {
		return nil, err
	}
	conn := FileConn(os.Stdin, os.Stdout)
	keepStdout()
	return conn, nil
}
