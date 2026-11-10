package plugin

import (
	"net"
	"os"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// FileConn is a net.Conn over two files: what the link reads from and what it writes to. The host
// makes it from its ends of the pipes it starts a plugin with, and Serve from the plugin's stdin and
// stdout.
//
// golib's rpc.Client bounds every write with a deadline, so the connection must take one on every
// OS. golib's StreamConn gives it: a file with deadlines of its own uses them (os.Pipe's on Unix,
// and the duplicates stdio_unix.go makes), and one without them (Windows' anonymous pipes) gets
// them through a goroutine. Close closes both.
func FileConn(r, w *os.File) net.Conn { return golibrpc.StreamConn(r, w) }
