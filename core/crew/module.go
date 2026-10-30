package crew

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// protocol is the protocol the crew verbs arrive in: the next free one when they merge (ADR
// 1793271500 §4.1), set then; rpc's Protocol and its catalog test move with it.
const protocol = 34

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
	exitParams struct {
		Scope   string `param:"scope"`
		Run     string `param:"run"`
		Epoch   int64  `param:"epoch"`
		Session string `param:"session"`
		Why     string `param:"why,optional" doc:"exit (the default) or start_failed"`
	}
)

// epochResult is crew.claim's and crew.takeover's answer.
type epochResult struct {
	Epoch int64 `json:"epoch"`
}

// Verbs are the crew module's verbs. The host's are LocalPeer and AudienceUser: a host process on
// the daemon's socket, never an agent (§4.4).
func (m *Manager) Verbs() []module.Verb {
	host := func(verb, result, doc string, codes ...int64) module.Contract {
		return module.Contract{Verb: verb, Since: protocol, Scope: module.ScopeLocalPeer, Audience: module.AudienceUser,
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
		module.Handle(module.Contract{Verb: "crew.whoami", Since: protocol, Audience: module.AudienceAgent,
			Effect: module.EffectRead, Result: "{codename, scope, profile, session, store}",
			Errors: []int64{CodeBadCredential, CodeContextMismatch, CodeStaleEpoch}, Doc: "Who the credential belongs to (§4.5)."},
			func(ctx context.Context, p whoamiParams) (Identity, error) {
				return m.Whoami(ctx, p.Credential, p.Store)
			}),
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
