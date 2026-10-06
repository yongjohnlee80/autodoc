package plugin

import (
	"bufio"
	"context"
	"net"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// MaxMessageBytes bounds one notification either way: a frame of a dialog is a few kilobytes.
const MaxMessageBytes = 1 << 20

// MaxDocumentBytes bounds a whole plugin.document as the link encodes it. It is the link's own
// limit: EncodedSize measures the notification exactly as the link writes it, envelope and all, and
// the link takes a message of MaxMessageBytes and refuses one byte more. A note whose document is
// larger is sent as too large, without its text (ADR 1791268009 §2.3).
const MaxDocumentBytes = MaxMessageBytes

// EncodedSize is the size of the notification method with params, as the link writes it.
func EncodedSize(method string, params []any) (int, error) {
	var n counter
	w := bufio.NewWriter(&n)
	m := &golibrpc.Message{Kind: golibrpc.KindNotification, Method: method, Params: params}
	if err := msgpackrpc.New(nil).Write(w, m); err != nil {
		return 0, err
	}
	if err := w.Flush(); err != nil {
		return 0, err
	}
	return int(n), nil
}

// FitDocument is d as it can be sent: d itself when its plugin.document encodes to at most
// MaxDocumentBytes, else its too-large form, which keeps its path, workspace and version alone.
func FitDocument(d Document) Document {
	if d.TooLarge {
		return d
	}
	if n, err := EncodedSize(MethodDocument, DocumentParams(d)); err == nil && n <= MaxDocumentBytes {
		return d
	}
	return Document{Path: d.Path, Workspace: d.Workspace, Version: d.Version, TooLarge: true}
}

type counter int

func (c *counter) Write(b []byte) (int, error) { *c += counter(len(b)); return len(b), nil }

// WriteTimeout bounds one send: a peer that has not read for this long is not answering.
const WriteTimeout = 2 * time.Second

// Link is one end of the protocol: notifications both ways over conn, the host's or the plugin's.
// Incoming ones are passed to on, one at a time in arrival order, from a goroutine of the link's.
// A burst larger than the link's queue (a paste of keys, say) waits for on to take them rather
// than failing the link, so on must not wait on the link itself: it never Calls, as the protocol
// has only notifications, and it should hand each one on quickly, as the peer's writes slow to its
// pace. Notify is safe from any goroutine.
type Link struct{ c *golibrpc.Client }

// NewLink starts the link over conn. It owns conn from here: Close closes it.
func NewLink(ctx context.Context, conn net.Conn, on func(method string, params []any)) (*Link, error) {
	c, err := golibrpc.Dial(ctx, "plugin", msgpackrpc.New(nil),
		golibrpc.WithConnDialer(func(context.Context, string, string) (net.Conn, error) { return conn, nil }),
		golibrpc.OnNotification(on), golibrpc.NotificationBuffer(1024), golibrpc.NotificationBackpressure(),
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
