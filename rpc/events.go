package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/yongjohnlee80/golib/logger"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// EVENTS — what one client changed, for the others to see (ADR 0212 §7).
//
// Every connection is given a client token at sys.hello. A verb that changes configuration or a
// workspace's lifecycle logs an event once it succeeds: its kind, the workspace it is about, the
// token of the client that asked, and a short detail (a name; never a secret, never a file's
// content). A client follows the log with sys.events, as it follows index.changes, and tells its
// user about the events other clients caused. The server still sends nothing unasked: the log is
// pulled.

// Events is the daemon-wide event log (core/store's).
type Events interface {
	AppendEvent(ctx context.Context, e store.Event) (int64, error)
	Events(ctx context.Context, since int64, limit int) ([]store.Event, int64, bool, error)
}

// WithEvents keeps the event log in ev; without it, sys.events answers that the server keeps none.
func WithEvents(ev Events) Option { return func(o *options) { o.events = ev } }

// sessClient is the session's client token.
const sessClient = "client"

// maxEvents bounds one sys.events page.
const maxEvents = 500

// newClientToken is a session's token: the client's own name and a random suffix.
func newClientToken(name string) string {
	if name == "" {
		name = "client"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return name + "#" + hex.EncodeToString(b[:])
}

func clientOf(sess *golibrpc.Session) string {
	if sess == nil {
		return ""
	}
	c, _ := sess.Value(sessClient).(string)
	return c
}

// logged wraps a verb's handler: once it succeeds, the events its contract names are logged, with
// the session's client token. A log that fails is reported in the server's log and does not fail
// the verb, which has taken effect.
func (s *Server) logged(v module.Verb, h golibrpc.Handler) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		out, err := h(ctx, req)
		if err != nil || s.events == nil {
			return out, err
		}
		for _, e := range v.EventsOf(ctx, req.Params, out) {
			se := store.Event{Kind: e.Kind, Workspace: e.Workspace, Detail: e.Detail, Client: clientOf(req.Session)}
			if _, lerr := s.events.AppendEvent(context.WithoutCancel(ctx), se); lerr != nil {
				logger.Warning(s.log, lerr, "logging event "+se.Kind)
			}
		}
		return out, nil
	}
}
