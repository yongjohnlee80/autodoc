package inbox

import (
	"context"
	"time"

	"github.com/yongjohnlee80/golib/msgpack"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// protocol is the protocol the inbox verbs arrive in: crew's and mail's, as one change (ADR
// 1793271500 §4.1).
const protocol = crew.Protocol

// The verbs' params, in their positional order.
type (
	watchParams struct {
		Cursor  int64  `param:"cursor" doc:"the last answer's cursor; 0 for a snapshot"`
		Timeout int64  `param:"timeout,optional" doc:"milliseconds to wait for a change, at most 25000"`
		Page    string `param:"page,optional" doc:"a snapshot's next: the rest of that snapshot, answered at once, its cursor echoed"`
	}
	listParams struct {
		Filter string `param:"filter,optional" doc:"\"\" for the history, or undeliverable"`
		Before int64  `param:"before,optional" doc:"the history's seq to page below; 0 for the newest"`
		Limit  int64  `param:"limit,optional" doc:"at most 200; default 50"`
	}
	replyParams struct {
		Message string `param:"message" doc:"the request's id"`
		Body    string `param:"body"`
		Key     string `param:"key" doc:"the idempotency key: a retry with the same body answers the first reply"`
	}
	messageParams struct {
		Message string `param:"message"`
	}
	focusParams struct {
		Session string `param:"session" doc:"the session whose agent to raise"`
	}
)

// The verbs' answers, as the wire carries them. Times are RFC 3339; "" is none.
type (
	wireItem struct {
		Message  string `json:"message"`
		Kind     string `json:"kind"` // message (a report) | request | response | event
		From     string `json:"from"` // the sender's codename
		Scope    string `json:"scope"`
		Session  string `json:"session"` // the sender's session, for a window to find its agent
		Subject  string `json:"subject"`
		Body     string `json:"body"`
		ReplyTo  string `json:"reply_to"`
		State    string `json:"state"` // open | answered | dismissed | read | expired
		Created  string `json:"created"`
		Expires  string `json:"expires"`
		Delivery string `json:"delivery"`
		Seq      int64  `json:"seq"`
		Status   string `json:"status"` // the delivery's: queued | acked | failed …
		Why      string `json:"why"`
	}
	wireAttention struct {
		Session  string `json:"session"`
		Codename string `json:"codename"`
		Scope    string `json:"scope"`
		Owner    string `json:"owner"` // the host whose terminal shows the prompt
		Kind     string `json:"kind"`  // permission | blocked
		Text     string `json:"text"`
		Raised   string `json:"raised"`
	}
	wireChange struct {
		Seq  int64  `json:"seq"`
		Kind string `json:"kind"`
		Ref  string `json:"ref"`
		At   string `json:"at"`
	}
	wireSnapshot struct {
		Items     []wireItem      `json:"items"`
		Attention []wireAttention `json:"attention"`
		Next      string          `json:"next"` // not "": more of the snapshot; watch again with page = next
	}
	watchResult struct {
		Cursor   int64         `json:"cursor"`
		Snapshot *wireSnapshot `json:"snapshot,omitempty"`
		Changes  []wireChange  `json:"changes"`
	}
	listResult struct {
		Items []wireItem `json:"items"`
	}
	replyResult struct {
		Message string `json:"message"` // the response's id
	}
	focusResult struct {
		Session string `json:"session"`
		Owner   string `json:"owner"`
	}
)

func stamp(ns int64) string { return time.Unix(0, ns).UTC().Format(time.RFC3339Nano) }

func stampOf(ns *int64) string {
	if ns == nil {
		return ""
	}
	return stamp(*ns)
}

func toWireItem(it Item) wireItem {
	m, d := it.Message, it.Delivery
	return wireItem{Message: m.ID, Kind: m.Kind, From: it.Codename, Scope: it.Scope,
		Session: m.SenderSession, Subject: m.Subject, Body: m.Body, ReplyTo: m.ReplyTo, State: m.State,
		Created: stamp(m.Created), Expires: stampOf(m.Expires), Delivery: d.ID, Seq: d.Seq, Status: d.State,
		Why: d.Why}
}

func toWireItems(items []Item) []wireItem {
	out := make([]wireItem, 0, len(items))
	for _, it := range items {
		out = append(out, toWireItem(it))
	}
	return out
}

func toWireAttention(a Attention) wireAttention {
	return wireAttention{Session: a.Session, Codename: a.Codename, Scope: a.Scope, Owner: a.Owner, Kind: a.Kind,
		Text: a.Text, Raised: stamp(a.Raised)}
}

// wireSize is an entry's size exactly as the RPC writes it: the host's wire form (module.Wire) in
// the same msgpack encoding (golib's msgpack, which the RPC codec uses), every field included.
func wireSize(entry any) int {
	w, err := module.Wire(entry)
	if err != nil {
		return pageBytes + 1 // can't happen for these structs; never let it pass as small
	}
	b, err := msgpack.Marshal(w)
	if err != nil {
		return pageBytes + 1
	}
	return len(b)
}

func toWireWatched(w Watched) watchResult {
	out := watchResult{Cursor: w.Cursor, Changes: make([]wireChange, 0, len(w.Changes))}
	for _, c := range w.Changes {
		out.Changes = append(out.Changes, wireChange{Seq: c.Seq, Kind: c.Kind, Ref: c.Ref, At: stamp(c.At)})
	}
	if s := w.Snapshot; s != nil {
		snap := wireSnapshot{Items: toWireItems(s.Items), Attention: make([]wireAttention, 0, len(s.Attention)), Next: s.Next}
		for _, a := range s.Attention {
			snap.Attention = append(snap.Attention, toWireAttention(a))
		}
		out.Snapshot = &snap
	}
	return out
}

// listDefault is inbox.list's limit when the caller gives none.
const listDefault = 50

// Verbs are the inbox module's verbs: the human's, LocalPeer, with no credential (§4.8).
func (m *Manager) Verbs() []module.Verb {
	user := func(verb string, effect module.Effect, result, doc string, codes ...int64) module.Contract {
		return module.Contract{Verb: verb, Since: protocol, Scope: module.ScopeLocalPeer, Audience: module.AudienceUser,
			Effect: effect, Result: result, Errors: codes, Doc: doc}
	}
	return []module.Verb{
		module.Handle(user("inbox.watch", module.EffectRead, "{cursor, snapshot?{items, attention, next}, changes[]}",
			"The human feed: the changes after cursor, waiting up to timeout for one; a snapshot of what is open when the cursor is 0 or no longer retained, its attention then its open items, at most 500 entries and 2 MiB a page (page = next for the rest). Never leases. A connection holds at most two long-polls.",
			crew.CodeLongPollsBusy),
			func(ctx context.Context, p watchParams) (watchResult, error) {
				leave, err := m.polls.Enter(module.ConnFrom(ctx))
				if err != nil {
					return watchResult{}, err
				}
				defer leave()
				w, err := m.Watch(ctx, p.Cursor, time.Duration(p.Timeout)*time.Millisecond, p.Page)
				return toWireWatched(w), err
			}),
		module.Handle(user("inbox.list", module.EffectRead, "{items[]}",
			"The human's history, newest first, or the generated notices nobody could receive (filter undeliverable)."),
			func(ctx context.Context, p listParams) (listResult, error) {
				limit := int(p.Limit)
				if limit == 0 {
					limit = listDefault
				}
				items, err := m.List(ctx, p.Filter, p.Before, limit)
				return listResult{Items: toWireItems(items)}, err
			}),
		module.Handle(user("inbox.reply", module.EffectWrite, "{message}",
			"Answer a request; the first answer wins. The key is bound to the request and the body: a retry after a lost answer returns the same response.",
			CodeAlreadyAnswered, CodeNotARequest, CodeExpired, CodeKeyReused, CodeTooLarge),
			func(ctx context.Context, p replyParams) (replyResult, error) {
				r, err := m.Reply(ctx, p.Message, p.Body, p.Key)
				return replyResult{Message: r.ID}, err
			}),
		module.Handle(user("inbox.dismiss", module.EffectWrite, "{}",
			"Decline a request; its sender is told. Replaying a dismiss answers ok.",
			CodeAlreadyAnswered, CodeNotARequest, CodeExpired),
			func(ctx context.Context, p messageParams) (struct{}, error) {
				return struct{}{}, m.Dismiss(ctx, p.Message)
			}),
		module.Handle(user("inbox.ack", module.EffectWrite, "{}", "Mark a report read. Replaying an ack answers ok.", CodeExpired),
			func(ctx context.Context, p messageParams) (struct{}, error) {
				return struct{}{}, m.Ack(ctx, p.Message)
			}),
		module.Handle(user("inbox.focus", module.EffectWrite, "{session, owner}",
			"Ask the session's current owner to raise its agent: the owner's host hears a focus change.", codeNotFound),
			func(ctx context.Context, p focusParams) (focusResult, error) {
				f, err := m.Focus(ctx, p.Session)
				return focusResult{Session: f.Session, Owner: f.Owner}, err
			}),
	}
}
