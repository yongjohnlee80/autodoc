package plugin

import (
	"errors"
	"net"
	"os"
	"time"
)

// FileConn is a net.Conn over two files: what the link reads from and what it writes to. The host
// makes it from its ends of the pipes it starts a plugin with, and Serve from the plugin's stdin and
// stdout.
//
// golib's rpc.Client bounds every write with a deadline and refuses a connection that cannot take
// one, so the files must be pollable: os.Pipe's are, and Serve makes stdin and stdout so
// (stdio_unix.go). Close closes both.
func FileConn(r, w *os.File) net.Conn { return &fileConn{r: r, w: w} }

type fileConn struct{ r, w *os.File }

func (c *fileConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *fileConn) Write(b []byte) (int, error) { return c.w.Write(b) }
func (c *fileConn) Close() error                { return errors.Join(c.w.Close(), c.r.Close()) }
func (c *fileConn) LocalAddr() net.Addr         { return pipeAddr{} }
func (c *fileConn) RemoteAddr() net.Addr        { return pipeAddr{} }

func (c *fileConn) SetDeadline(t time.Time) error {
	return errors.Join(c.r.SetReadDeadline(t), c.w.SetWriteDeadline(t))
}
func (c *fileConn) SetReadDeadline(t time.Time) error  { return c.r.SetReadDeadline(t) }
func (c *fileConn) SetWriteDeadline(t time.Time) error { return c.w.SetWriteDeadline(t) }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "plugin" }
