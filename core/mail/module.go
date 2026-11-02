package mail

import (
	"context"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// protocol is the protocol the mail verbs arrive in: crew's, as one change
// (docs/decisions/crew.md#three-modules-and-one-protocol).
const protocol = crew.Protocol

// fallbackSince is the protocol the fallback's host verbs arrive in (rpc's Protocol 42).
const fallbackSince = 42

// showSince is the protocol mail.show arrives in (rpc's Protocol 45).
const showSince = 45

// Name is the module's name: its verbs are mail.*.
func (m *Manager) Name() string { return "mail" }

// Errors are what the mail verbs may answer besides invalid params.
func (m *Manager) Errors() []module.PublicError {
	return []module.PublicError{
		{Err: ErrNoRecipient, Code: CodeNoRecipient, Name: "mail.no_recipient", Message: "an address names no active profile or live session",
			Next: "crew.list the addressbook, and send to a name it shows"},
		{Err: ErrAmbiguous, Code: CodeAmbiguous, Name: "mail.ambiguous", Message: "a bare name more than one other scope has a profile of",
			Next: "send to the one meant by name@scope, as the error names them"},
		{Err: ErrKeyReused, Code: CodeKeyReused, Name: "mail.key_reused", Message: "the key already names another message",
			Next: "a retry sends the same content; new content takes a new key"},
		{Err: ErrTooLarge, Code: CodeTooLarge, Name: "mail.too_large", Message: "the body is past 64 KiB",
			Next: "send a shorter body, and put the rest in a file the recipient can read"},
		{Err: ErrLeaseLost, Code: CodeLeaseLost, Name: "mail.lease_lost", Message: "the caller no longer holds the delivery's lease",
			Next: "mail.receive again: the delivery comes back if it is still unhandled"},
		{Err: crew.ErrLongPollsBusy, Code: crew.CodeLongPollsBusy, Name: "mail.long_polls_busy",
			Message: "a connection holds at most two long-polls", Next: "finish one first, or open another connection"},
	}
}

// The verbs' params, in their positional order. Every verb's first is the session's credential.
type (
	sendParams struct {
		Credential string   `param:"credential" doc:"the session context's \"<session id>.<secret>\""`
		To         []string `param:"to" doc:"names in the scope, name@scope, user, or session:<id>"`
		Body       string   `param:"body" doc:"at most 64 KiB"`
		Key        string   `param:"key" doc:"the idempotency key: a retry with the same content answers the first send"`
		Kind       string   `param:"kind,optional" doc:"message (the default) or request"`
		Subject    string   `param:"subject,optional"`
		TTL        int64    `param:"ttl,optional" doc:"seconds until the message expires; 0 never"`
	}
	credentialParams struct {
		Credential string `param:"credential"`
	}
	leaseParams struct {
		Credential string `param:"credential"`
		Delivery   string `param:"delivery"`
		Lease      string `param:"lease" doc:"the lease mail.receive gave"`
	}
	replyParams struct {
		Credential string `param:"credential"`
		Delivery   string `param:"delivery" doc:"the request's delivery"`
		Lease      string `param:"lease"`
		Body       string `param:"body"`
		Key        string `param:"key" doc:"the reply's idempotency key"`
		Subject    string `param:"subject,optional"`
	}
	waitParams struct {
		Credential string `param:"credential"`
		Timeout    int64  `param:"timeout,optional" doc:"milliseconds to wait for mail, at most 25000"`
	}
	fallbacksParams struct {
		Scope   string `param:"scope" doc:"the scope the host holds"`
		Run     string `param:"run" doc:"the host's run token"`
		Epoch   int64  `param:"epoch" doc:"the host's epoch"`
		Timeout int64  `param:"timeout,optional" doc:"milliseconds to wait for a job, at most 25000"`
	}
	fallbackDoneParams struct {
		Scope   string `param:"scope"`
		Run     string `param:"run"`
		Epoch   int64  `param:"epoch"`
		Job     string `param:"job"`
		Lease   string `param:"lease" doc:"the lease mail.fallbacks gave"`
		Outcome string `param:"outcome" doc:"delivered, typed_not_sent, or unknown"`
	}
	inboxParams struct {
		Credential string `param:"credential"`
		Limit      int64  `param:"limit,optional" doc:"items to show, at most 1000; 0 counts only"`
	}
)

// The wire's shapes.
type (
	wireMessage struct {
		ID      string `json:"id"`
		Sender  string `json:"sender"`
		Kind    string `json:"kind"`
		Subject string `json:"subject,omitempty"`
		Body    string `json:"body"`
		ReplyTo string `json:"reply_to,omitempty"`
		Created string `json:"created"`
		Expires string `json:"expires,omitempty"`
	}
	wireDelivery struct {
		ID         string `json:"id"`
		Recipient  string `json:"recipient"`
		Session    string `json:"session,omitempty"`
		Seq        int64  `json:"seq"`
		State      string `json:"state"`
		Lease      string `json:"lease,omitempty"`
		LeaseUntil string `json:"lease_until,omitempty"`
		Attempts   int64  `json:"attempts"`
		Why        string `json:"why,omitempty"`
	}
	wireItem struct {
		From     string       `json:"from"` // the sender as an address the receiver can reply to
		Delivery wireDelivery `json:"delivery"`
		Message  wireMessage  `json:"message"`
	}
	sentResult struct {
		Message    wireMessage    `json:"message"`
		Deliveries []wireDelivery `json:"deliveries"`
	}
	receivedResult struct {
		Deliveries []wireItem `json:"deliveries"`
		More       bool       `json:"more"`
	}
	inboxResult struct {
		Unread int        `json:"unread"`
		Items  []wireItem `json:"items"`
	}
	replyResult struct {
		Message wireMessage `json:"message"`
	}
	wireJob struct {
		ID         string      `json:"id"`
		Lease      string      `json:"lease"`
		LeaseUntil string      `json:"lease_until"`
		Tries      int64       `json:"tries"` // leases of this job before this one: a retry knows it is one
		Recipient  string      `json:"recipient"`
		Codename   string      `json:"codename"` // the recipient's
		Kind       string      `json:"kind"`     // the recipient's CLI kind
		From       string      `json:"from"`     // the sender as the recipient would address an answer
		Message    wireMessage `json:"message"`
		Text       string      `json:"text"` // what the host types or files for the recipient (FallbackText)
	}
	fallbacksResult struct {
		Jobs []wireJob `json:"jobs"`
	}
	readyResult struct {
		Ready    bool `json:"ready"`
		TimedOut bool `json:"timed_out"`
	}
)

func stamp(ns int64) string { return time.Unix(0, ns).UTC().Format(time.RFC3339Nano) }

func stampOf(ns *int64) string {
	if ns == nil {
		return ""
	}
	return stamp(*ns)
}

func toWireMessage(m Message) wireMessage {
	return wireMessage{ID: m.ID, Sender: m.Sender, Kind: m.Kind, Subject: m.Subject, Body: m.Body, ReplyTo: m.ReplyTo,
		Created: stamp(m.Created), Expires: stampOf(m.Expires)}
}

func toWireDelivery(d Delivery) wireDelivery {
	return wireDelivery{ID: d.ID, Recipient: d.Recipient, Session: d.Session, Seq: d.Seq, State: d.State, Lease: d.Lease,
		LeaseUntil: stampOf(d.LeaseUntil), Attempts: d.Attempts, Why: d.Why}
}

func toWireJobs(offers []Offer) []wireJob {
	out := make([]wireJob, 0, len(offers))
	for _, o := range offers {
		out = append(out, wireJob{ID: o.Job.ID, Lease: o.Job.Lease, LeaseUntil: stampOf(o.Job.LeaseUntil), Tries: o.Job.Tries,
			Recipient: o.Job.Recipient, Codename: o.Codename, Kind: o.Kind, From: o.From, Message: toWireMessage(o.Message),
			Text: FallbackText(o.From, o.Message)})
	}
	return out
}

func toWireItems(items []Item) []wireItem {
	out := make([]wireItem, 0, len(items))
	for _, it := range items {
		out = append(out, toWireItem(it))
	}
	return out
}

func toWireItem(it Item) wireItem {
	return wireItem{From: it.From, Delivery: toWireDelivery(it.Delivery), Message: toWireMessage(it.Message)}
}

// Verbs are the mail module's verbs: an agent's, on its own credential
// (docs/decisions/crew.md#durable-mail).
func (m *Manager) Verbs() []module.Verb {
	agent := func(verb string, effect module.Effect, result, doc string, codes ...int64) module.Contract {
		return module.Contract{Verb: verb, Since: protocol, Audience: module.AudienceAgent, Effect: effect,
			Result: result, Errors: append([]int64{crew.CodeBadCredential, crew.CodeStaleEpoch}, codes...), Doc: doc}
	}
	// a host's: the scope it holds, its run token and epoch, guarded in the write as crew's are
	host := func(verb, result, doc string, codes ...int64) module.Contract {
		return module.Contract{Verb: verb, Since: fallbackSince, Scope: module.ScopeLocalPeer, Audience: module.AudienceUser,
			Effect: module.EffectWrite, Result: result, Errors: append([]int64{crew.CodeStaleEpoch}, codes...), Doc: doc}
	}
	show := agent("mail.show", module.EffectRead, "{from, delivery, message}",
		"A delivery the caller holds, read again (a body its reader cut short): its message under the lease mail.receive gave, the lease, lease_until and attempts unchanged.",
		CodeLeaseLost)
	show.Since = showSince
	return []module.Verb{
		module.Handle(agent("mail.send", module.EffectWrite, "{message, deliveries[]}",
			"Store a message and its deliveries in one transaction. The same key answers the first result while the message is retained.",
			CodeNoRecipient, CodeAmbiguous, CodeKeyReused, CodeTooLarge),
			func(ctx context.Context, p sendParams) (sentResult, error) {
				s, err := m.Send(ctx, p.Credential, Send{To: p.To, Kind: p.Kind, Subject: p.Subject, Body: p.Body, Key: p.Key,
					TTL: time.Duration(p.TTL) * time.Second})
				if err != nil {
					return sentResult{}, err
				}
				out := sentResult{Message: toWireMessage(s.Message), Deliveries: []wireDelivery{}}
				for _, d := range s.Deliveries {
					out.Deliveries = append(out.Deliveries, toWireDelivery(d))
				}
				return out, nil
			}),
		module.Handle(agent("mail.receive", module.EffectWrite, "{deliveries[], more}",
			"Lease the caller's next eligible deliveries, at most 8, in seq order; more=true until drained."),
			func(ctx context.Context, p credentialParams) (receivedResult, error) {
				r, err := m.Receive(ctx, p.Credential)
				return receivedResult{Deliveries: toWireItems(r.Items), More: r.More}, err
			}),
		module.Handle(agent("mail.renew", module.EffectWrite, "{}", "Extend a lease the caller holds by 10 minutes.", CodeLeaseLost),
			func(ctx context.Context, p leaseParams) (struct{}, error) {
				return struct{}{}, m.Renew(ctx, p.Credential, p.Delivery, p.Lease)
			}),
		module.Handle(show,
			func(ctx context.Context, p leaseParams) (wireItem, error) {
				it, err := m.Show(ctx, p.Credential, p.Delivery, p.Lease)
				if err != nil {
					return wireItem{}, err
				}
				return toWireItem(it), nil
			}),
		module.Handle(agent("mail.ack", module.EffectWrite, "{}",
			"Done with a delivery under its lease. Replaying an ack of the same lease answers ok.", CodeLeaseLost),
			func(ctx context.Context, p leaseParams) (struct{}, error) {
				return struct{}{}, m.Ack(ctx, p.Credential, p.Delivery, p.Lease)
			}),
		module.Handle(agent("mail.reply", module.EffectWrite, "{message}",
			"Ack a request's delivery and send its response, in one transaction. One response per request: a retry with the same content answers it.",
			CodeLeaseLost, CodeKeyReused, CodeTooLarge),
			func(ctx context.Context, p replyParams) (replyResult, error) {
				msg, err := m.Reply(ctx, p.Credential, p.Delivery, p.Lease, Reply{Subject: p.Subject, Body: p.Body, Key: p.Key})
				return replyResult{Message: toWireMessage(msg)}, err
			}),
		module.Handle(agent("mail.wait", module.EffectRead, "{ready, timed_out}",
			"Answers when the caller has mail it may receive, or at timeout (at most 25 s). A connection holds at most two long-polls.",
			crew.CodeLongPollsBusy),
			func(ctx context.Context, p waitParams) (readyResult, error) {
				leave, err := m.polls.Enter(module.ConnFrom(ctx))
				if err != nil {
					return readyResult{}, err
				}
				defer leave()
				r, err := m.Wait(ctx, p.Credential, time.Duration(p.Timeout)*time.Millisecond)
				return readyResult{Ready: r.Ready, TimedOut: r.TimedOut}, err
			}),
		module.Handle(host("mail.fallbacks", "{jobs[]}",
			"The host's fallback jobs: deliveries its scope's agents did not receive in time, leased to it for 60 s, each with its message, sender and recipient, answered as soon as there is one or at timeout (at most 25 s). Calling it is what makes the host take jobs, in its current epoch; a host that never calls it keeps its agents' mail queued. A job offered again (its lease lapsed) has tries above 0: deliver it only if the host's own record says it hasn't, else report it. A connection holds at most two long-polls.",
			crew.CodeLongPollsBusy),
			func(ctx context.Context, p fallbacksParams) (fallbacksResult, error) {
				leave, err := m.polls.Enter(module.ConnFrom(ctx))
				if err != nil {
					return fallbacksResult{}, err
				}
				defer leave()
				offers, err := m.Fallbacks(ctx, p.Scope, p.Run, p.Epoch, time.Duration(p.Timeout)*time.Millisecond)
				return fallbacksResult{Jobs: toWireJobs(offers)}, err
			}),
		module.Handle(host("mail.fallback_done", "{}",
			"Report what came of a leased job: delivered, typed_not_sent (in the agent's input, waiting on the user's Enter), or unknown (the host can't tell; the user is shown the text). Once: replaying the same lease's report answers ok.",
			CodeLeaseLost),
			func(ctx context.Context, p fallbackDoneParams) (struct{}, error) {
				return struct{}{}, m.FallbackDone(ctx, p.Scope, p.Run, p.Epoch, p.Job, p.Lease, FallbackOutcome(p.Outcome))
			}),
		module.Handle(agent("mail.inbox", module.EffectRead, "{unread, items[]}", "A preview of the caller's eligible mail: never leases."),
			func(ctx context.Context, p inboxParams) (inboxResult, error) {
				in, err := m.Preview(ctx, p.Credential, int(p.Limit))
				return inboxResult{Unread: in.Unread, Items: toWireItems(in.Items)}, err
			}),
	}
}
