package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/yongjohnlee80/golib/logger"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

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

// eventSpec is what a verb's success logs: the kind, and which parameters name the workspace and
// the detail (-1 for none).
type eventSpec struct {
	kind              string
	workspace, detail int
}

// eventsOf are the verbs that log an event, by name.
var eventsOf = map[string]eventSpec{
	"workspace.add":                 {"workspace.added", 0, -1},
	"workspace.rename":              {"workspace.renamed", 0, 1},
	"workspace.remove":              {"workspace.removed", 0, -1},
	"workspace.set_patterns":        {"workspace.patterns", 0, -1},
	"workspace.set_schema":          {"workspace.schema", 0, -1},
	"workspace.set_text_extensions": {"workspace.text_extensions", 0, -1},
	"workspace.section_size":        {"workspace.section_size", 0, -1},
	"workspace.embedding_policy":    {"workspace.embedding_policy", 0, 1},
	"workspace.set_provider":        {"workspace.provider", 0, 1},
	"workspace.configure":           {"workspace.configured", 0, -1},
	"preference.set":                {"preference.changed", -1, 0},
	"embedding.use":                 {"embedding.switched", -1, 0},
	"embedding.cancel_switch":       {"embedding.cancelled", -1, -1},
	"embedding.add":                 {"embedding.providers", -1, -1},
	"embedding.update":              {"embedding.providers", -1, 0},
	"embedding.remove":              {"embedding.providers", -1, 0},
}

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

// logged wraps a verb's handler: once it succeeds, the event its spec names is logged. A log that
// fails is reported in the server's log and does not fail the verb, which has taken effect.
func (s *Server) logged(spec eventSpec, h golibrpc.Handler) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		out, err := h(ctx, req)
		if err != nil || s.events == nil {
			return out, err
		}
		e := store.Event{Kind: spec.kind, Client: clientOf(req.Session)}
		if spec.workspace >= 0 && spec.workspace < len(req.Params) {
			e.Workspace, _ = req.Params[spec.workspace].(string)
		}
		if spec.detail >= 0 && spec.detail < len(req.Params) {
			e.Detail, _ = req.Params[spec.detail].(string)
		}
		if _, lerr := s.events.AppendEvent(context.WithoutCancel(ctx), e); lerr != nil {
			logger.Warning(s.log, lerr, "logging event "+spec.kind)
		}
		return out, nil
	}
}

func (s *Server) registerEvents() {
	s.handle("sys.events", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if s.events == nil {
			return nil, errNoEvents
		}
		since, err := argInt(p, 0, "since")
		if err != nil {
			return nil, err
		}
		limit, err := argInt(p, 1, "limit")
		if err != nil {
			return nil, err
		}
		if limit < 1 || limit > maxEvents {
			return nil, invalid("sys.events: limit must be 1 to 500")
		}
		evs, cursor, more, err := s.events.Events(ctx, since, int(limit))
		if err != nil {
			return nil, err
		}
		out := make([]any, len(evs))
		for i, e := range evs {
			out[i] = map[string]any{"seq": e.Seq, "kind": e.Kind, "workspace": e.Workspace, "client": e.Client,
				"detail": e.Detail, "at": e.At}
		}
		return map[string]any{"cursor": cursor, "events": out, "more": more}, nil
	}, false))
}
