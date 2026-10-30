package crew

import (
	"context"
	"fmt"
	"time"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// Protocol is the protocol the crew, mail and inbox verbs arrive in, as one change (ADR 1793271500
// §4.1): the one place it is set. It follows #50's 34 and AP-5's and AP-6's 35 and 36; rpc's
// Protocol and its catalog test move with it.
const Protocol = 37

// Name is the module's name: its verbs are crew.*.
func (m *Manager) Name() string { return "crew" }

// Errors are what the crew verbs may answer besides invalid params.
func (m *Manager) Errors() []module.PublicError {
	return []module.PublicError{
		{Err: ErrScopeBusy, Code: CodeScopeBusy, Name: "crew.scope_busy", Message: "another live owner holds the scope",
			Next: "wait for its lease to lapse, or crew.takeover after the user agrees"},
		{Err: ErrStaleEpoch, Code: CodeStaleEpoch, Name: "crew.stale_epoch", Message: "the scope changed hands since this epoch",
			Next: "claim the scope again; this owner was fenced"},
		{Err: ErrCodenameBusy, Code: CodeCodenameBusy, Name: "crew.codename_busy", Message: "the codename already has a live session",
			Next: "offer the user to take it over, or pick another codename"},
		{Err: ErrInstanceReused, Code: CodeInstanceReused, Name: "crew.instance_reused",
			Message: "this instance was registered with another secret, codename, kind or workspace, or has ended",
			Next:    "register a new instance"},
		{Err: ErrBadCredential, Code: CodeBadCredential, Name: "crew.bad_credential", Message: "no such live session",
			Next: "the session ended: its host registers a new one"},
		{Err: ErrContextMismatch, Code: CodeContextMismatch, Name: "crew.context_mismatch",
			Message: "two session contexts disagree, or one is for another store", Next: "fix AUTODOC_CREW_CONTEXT or the working directory's .autodoc-crew"},
		{Err: ErrBadEndpoint, Code: CodeBadEndpoint, Name: "crew.bad_endpoint", Message: "the daemon refuses that endpoint",
			Next: "register an endpoint of a known kind, with its locator"},
		{Err: ErrLongPollsBusy, Code: CodeLongPollsBusy, Name: "crew.long_polls_busy",
			Message: "a connection holds at most two long-polls", Next: "finish one first, or open another connection"},
	}
}

// The verbs' params, in their positional order.
type (
	claimParams struct {
		Scope string `param:"scope" doc:"autovim:<project>, nvim:<key> or autodoc:<id>"`
		Run   string `param:"run" doc:"the host process's run token, held in memory only"`
		Proc  string `param:"proc,optional" doc:"<boot id>:<pid ns>:<pid>:<start time>; \"\" when unknown"`
	}
	hostParams struct {
		Scope string `param:"scope"`
		Run   string `param:"run"`
		Epoch int64  `param:"epoch"`
	}
	registerParams struct {
		Scope     string `param:"scope"`
		Run       string `param:"run"`
		Epoch     int64  `param:"epoch"`
		Codename  string `param:"codename"`
		Kind      string `param:"kind" doc:"claude, codex, agy, opencode …"`
		Workspace string `param:"workspace" doc:"the workspace's uid"`
		Instance  string `param:"instance" doc:"the host's spawn id"`
		Secret    string `param:"secret" doc:"host-generated, 32+ random bytes; only its hash is kept"`
	}
	whoamiParams struct {
		Credential string `param:"credential" doc:"the session context's \"<session id>.<secret>\""`
		Store      string `param:"store,optional" doc:"the store id the session context names"`
	}
	adoptParams struct {
		From  string `param:"from" doc:"the orphan scope (crew.orphans)"`
		To    string `param:"to" doc:"the caller's own scope"`
		Run   string `param:"run"`
		Epoch int64  `param:"epoch" doc:"the caller's epoch for to"`
	}
	endpointParams struct {
		Credential string `param:"credential"`
		Kind       string `param:"kind" doc:"claude, codex, agy or opencode: the session's own"`
		Locator    string `param:"locator" doc:"JSON, kind-specific; never a secret"`
	}
	waitParams struct {
		Scope    string   `param:"scope"`
		Run      string   `param:"run"`
		Epoch    int64    `param:"epoch"`
		Sessions []string `param:"sessions" doc:"the host's tier-3 sessions to wait on"`
		Timeout  int64    `param:"timeout,optional" doc:"milliseconds to wait for mail, at most 25000"`
	}
	attentionParams struct {
		Credential string `param:"credential"`
		Op         string `param:"op" doc:"raise or clear"`
		Kind       string `param:"kind,optional" doc:"for raise: permission or blocked"`
		Text       string `param:"text,optional" doc:"for raise: what the session waits on"`
	}
	guideParams struct {
		Kind string `param:"kind" doc:"the CLI: claude, codex, agy, opencode …"`
	}
	exitParams struct {
		Scope   string `param:"scope"`
		Run     string `param:"run"`
		Epoch   int64  `param:"epoch"`
		Session string `param:"session"`
		Why     string `param:"why,optional" doc:"exit (the default) or start_failed"`
	}
)

// pendingJSON and waitedResult are crew.wait's answer.
type (
	pendingJSON struct {
		Session   string `json:"session"`
		Unhandled int    `json:"unhandled"` // its profile's queued and leased deliveries
	}
	waitedResult struct {
		Ready    []pendingJSON `json:"ready"`
		TimedOut bool          `json:"timed_out"`
	}
)

// generationResult is crew.endpoint's answer.
type generationResult struct {
	Generation int64 `json:"generation"`
}

// epochResult is crew.claim's and crew.takeover's answer.
type epochResult struct {
	Epoch int64 `json:"epoch"`
}

// Verbs are the crew module's verbs. The host's are LocalPeer and AudienceUser: a host process on
// the daemon's socket, never an agent (§4.4).
func (m *Manager) Verbs() []module.Verb {
	host := func(verb, result, doc string, codes ...int64) module.Contract {
		return module.Contract{Verb: verb, Since: Protocol, Scope: module.ScopeLocalPeer, Audience: module.AudienceUser,
			Effect: module.EffectWrite, Result: result, Errors: codes, Doc: doc}
	}
	return []module.Verb{
		module.Handle(host("crew.claim", "{epoch}",
			"Claim or reattach a scope with the process's run token (§4.3). Idempotent.", CodeScopeBusy),
			func(ctx context.Context, p claimParams) (epochResult, error) {
				e, err := m.Claim(ctx, p.Scope, p.Run, p.Proc)
				return epochResult{e}, err
			}),
		module.Handle(host("crew.takeover", "{epoch}",
			"Claim a held scope after the user agreed; the old owner is fenced and its sessions end."),
			func(ctx context.Context, p claimParams) (epochResult, error) {
				e, err := m.Takeover(ctx, p.Scope, p.Run, p.Proc)
				return epochResult{e}, err
			}),
		module.Handle(host("crew.release", "{}",
			"Give a scope back on a clean exit; the run can't reattach.", CodeStaleEpoch),
			func(ctx context.Context, p hostParams) (struct{}, error) {
				return struct{}{}, m.Release(ctx, p.Scope, p.Run, p.Epoch)
			}),
		module.Handle(host("crew.register", "{session, profile}",
			"Start a session for (scope, codename) with a host-generated secret. Idempotent on (host, epoch, instance, secret).",
			CodeStaleEpoch, CodeCodenameBusy, CodeInstanceReused),
			func(ctx context.Context, p registerParams) (Registered, error) {
				return m.Register(ctx, Registration{Scope: p.Scope, Run: p.Run, Epoch: p.Epoch, Codename: p.Codename,
					Kind: p.Kind, Workspace: p.Workspace, Instance: p.Instance, Secret: p.Secret})
			}),
		module.Handle(host("crew.renew", "{}", "The host still holds its scope: its lease runs another 90 s.", CodeStaleEpoch),
			func(ctx context.Context, p hostParams) (struct{}, error) {
				return struct{}{}, m.Renew(ctx, p.Scope, p.Run, p.Epoch)
			}),
		module.Handle(module.Contract{Verb: "crew.whoami", Since: Protocol, Audience: module.AudienceAgent,
			Effect: module.EffectRead, Result: "{codename, scope, profile, session, store}",
			Errors: []int64{CodeBadCredential, CodeContextMismatch, CodeStaleEpoch}, Doc: "Who the credential belongs to (§4.5)."},
			func(ctx context.Context, p whoamiParams) (Identity, error) {
				return m.Whoami(ctx, p.Credential, p.Store)
			}),
		module.Handle(module.Contract{Verb: "crew.orphans", Since: Protocol, Scope: module.ScopeLocalPeer,
			Audience: module.AudienceUser, Effect: module.EffectRead, Result: "[{scope, profiles, unread}]",
			Doc: "AutoDoc scopes with a free holder and unhandled mail (§4.3)."},
			func(ctx context.Context, _ struct{}) ([]Orphan, error) { return m.Orphans(ctx) }),
		module.Handle(host("crew.adopt", "{}", "Fence an orphan scope and move its mail here (§4.3).", CodeScopeBusy, CodeStaleEpoch),
			func(ctx context.Context, p adoptParams) (struct{}, error) {
				return struct{}{}, m.Adopt(ctx, p.From, p.To, p.Run, p.Epoch)
			}),
		module.Handle(host("crew.discard", "{}", "Fence an orphan scope and fail its mail, telling senders.", CodeScopeBusy, CodeStaleEpoch),
			func(ctx context.Context, p adoptParams) (struct{}, error) {
				return struct{}{}, m.Discard(ctx, p.From, p.To, p.Run, p.Epoch)
			}),
		module.Handle(module.Contract{Verb: "crew.endpoint", Since: Protocol, Audience: module.AudienceAgent,
			Effect: module.EffectWrite, Result: "{generation}", Errors: []int64{CodeBadCredential, CodeBadEndpoint, CodeStaleEpoch},
			Doc: "How to wake the calling session (§4.7)."},
			func(ctx context.Context, p endpointParams) (generationResult, error) {
				g, err := m.SetEndpoint(ctx, p.Credential, p.Kind, p.Locator)
				return generationResult{g}, err
			}),
		module.Handle(module.Contract{Verb: "crew.wait", Since: Protocol, Scope: module.ScopeLocalPeer,
			Audience: module.AudienceUser, Effect: module.EffectRead, Result: "{ready[{session, unhandled}], timed_out}",
			Errors: []int64{CodeStaleEpoch, CodeLongPollsBusy},
			Doc:    "One long-poll for the host's tier-3 sessions (at most 25 s): which have mail waiting. A connection holds at most two long-polls."},
			func(ctx context.Context, p waitParams) (waitedResult, error) {
				leave, err := m.polls.Enter(module.ConnFrom(ctx))
				if err != nil {
					return waitedResult{}, err
				}
				defer leave()
				w, err := m.Wait(ctx, p.Scope, p.Run, p.Epoch, p.Sessions, time.Duration(p.Timeout)*time.Millisecond)
				out := waitedResult{Ready: []pendingJSON{}, TimedOut: w.TimedOut}
				for _, r := range w.Ready {
					out.Ready = append(out.Ready, pendingJSON{Session: r.Session, Unhandled: r.Unhandled})
				}
				return out, err
			}),
		module.Handle(module.Contract{Verb: "crew.attention", Since: Protocol, Audience: module.AudienceAgent,
			Effect: module.EffectWrite, Result: "{}", Errors: []int64{CodeBadCredential, CodeStaleEpoch},
			Doc: "Raise or clear: the session waits on the user (§4.8)."},
			func(ctx context.Context, p attentionParams) (struct{}, error) {
				switch p.Op {
				case "raise":
					return struct{}{}, m.RaiseAttention(ctx, p.Credential, p.Kind, p.Text)
				case "clear":
					return struct{}{}, m.ClearAttention(ctx, p.Credential)
				}
				return struct{}{}, fmt.Errorf("%w: attention op %q, want raise or clear", ErrInvalid, p.Op)
			}),
		module.Handle(module.Contract{Verb: "crew.guide", Since: Protocol, Scope: module.ScopeLocalPeer,
			Audience: module.AudienceUser, Effect: module.EffectRead, Result: "{markdown, protocol}",
			Doc: "The crew and mail procedure for a CLI kind (§4.11), for a host's agent guide."},
			func(_ context.Context, p guideParams) (Guide, error) { return m.GuideFor(p.Kind), nil }),
		module.Handle(module.Contract{Verb: "crew.list", Since: Protocol, Audience: module.AudienceAgent,
			Effect: module.EffectRead, Result: "[{codename, scope, kind, presence, reachable}]", Doc: "The addressbook."},
			func(ctx context.Context, _ struct{}) ([]Entry, error) { return m.List(ctx) }),
		module.Handle(host("crew.exit", "{}",
			"A session ended; its bound deliveries fail (session_ended).", CodeStaleEpoch, CodeBadCredential),
			func(ctx context.Context, p exitParams) (struct{}, error) {
				why := p.Why
				if why == "" {
					why = EndedExit
				}
				return struct{}{}, m.Exit(ctx, p.Scope, p.Run, p.Epoch, p.Session, why)
			}),
	}
}
