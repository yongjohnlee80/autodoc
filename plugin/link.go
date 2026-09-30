package plugin

import (
	"context"
	"net"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// MaxMessageBytes bounds one notification either way: a frame of a dialog is a few kilobytes.
const MaxMessageBytes = 1 << 20

// WriteTimeout bounds one send: a peer that has not read for this long is not answering.
const WriteTimeout = 2 * time.Second

// Link is one end of the protocol: notifications both ways over conn, the host's or the plugin's.
// Incoming ones are passed to on, one at a time in arrival order, from a goroutine of the link's;
// on must not block, or the link's bounded queue fills and the link fails (golib rpc.Client's
// overflow). Notify is safe from any goroutine.
type Link struct{ c *golibrpc.Client }

// NewLink starts the link over conn. It owns conn from here: Close closes it.
func NewLink(ctx context.Context, conn net.Conn, on func(method string, params []any)) (*Link, error) {
	c, err := golibrpc.Dial(ctx, "plugin", msgpackrpc.New(nil),
		golibrpc.WithConnDialer(func(context.Context, string, string) (net.Conn, error) { return conn, nil }),
		golibrpc.OnNotification(on), golibrpc.NotificationBuffer(1024),
		golibrpc.ClientMaxMessageBytes(MaxMessageBytes), golibrpc.ClientWriteTimeout(WriteTimeout))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &Link{c: c}, nil
}

// Notify sends one notification.
func (l *Link) Notify(ctx context.Context, method string, params []any) error {
	return l.c.Notify(ctx, method, params...)
}

// Done is closed when the link ends: closed, or its peer gone.
func (l *Link) Done() <-chan struct{} { return l.c.Done() }

// Err is why the link ended, once Done is closed.
func (l *Link) Err() error { return l.c.Err() }

// Close ends the link and closes its connection.
func (l *Link) Close() error { return l.c.Close() }
