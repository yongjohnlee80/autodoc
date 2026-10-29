package tui

import (
	"context"
	"errors"
	"fmt"

	"github.com/yongjohnlee80/autodoc/script"
)

// WHO AN INVOCATION ACTS FOR (ADR 1792004705 §4.2a.2c). A gated script's call is bounded, by the
// daemon, to the workspace (fsbounds); its catalog runs are bounded the same way here. But the work a
// call starts runs later: a load or a save job, a conflict's re-save, a key or menu row from its
// binder. So who it acts for is taken on the loop when it begins (authorityNow) and carried with
// that work, never asked again once the script's host call has returned.

// authority is the user, or a gated script with the workspace current when its invocation began.
// The zero authority is no one's: work that reaches a check with it is refused (errNoAuthority).
type authority struct {
	script string
	grants script.Grants
	ws     string // the current workspace when it began: a gated write's within
	user   bool
	// verbs are the daemon's contracts as the loop had them when it began: a bounded call is padded
	// to its verb's own params off the loop, from this snapshot
	verbs map[string]luaVerb
	// left is closed once the TUI leaves ws: a gated call checks it as it is sent, off the loop, with
	// no read of loop-owned state
	left <-chan struct{}
}

// userAuthority is the user's: the only authority the user's own input runs with.
func userAuthority() authority { return authority{user: true} }

func (a authority) gated() bool { return a.script != "" }

// errNoAuthority is work that lost who it acts for: refused, never taken for the user's.
var errNoAuthority = errors.New("tui: this work lost who it acts for, so it is refused")

// errWorkspaceLeft is a gated script's work queued in a workspace the TUI has left since.
var errWorkspaceLeft = errors.New("the workspace changed: a script's queued work there was dropped")

// stillIn is nil while a's workspace is the one in use, errWorkspaceLeft once the TUI left it; any
// goroutine may ask. A gated authority that never took the channel is refused.
func (a authority) stillIn() error {
	if a.left == nil {
		return errWorkspaceLeft
	}
	select {
	case <-a.left:
		return errWorkspaceLeft
	default:
		return nil
	}
}

// noAuthority reports work with the zero authority; tests make it panic (authority_test.go), so a
// propagation forgotten is a red cell.
var noAuthority = func(what string) error { return fmt.Errorf("%s: %w", what, errNoAuthority) }

// check is nil for an authority that is someone's.
func (a authority) check(what string) error {
	if a.user || a.gated() {
		return nil
	}
	return noAuthority(what)
}

// authorityNow is who the invocation beginning now acts for, on the loop: a gated script's key or
// row while it runs its line (h.invoking), the gated script whose host call runs now
// (Engine.Caller: autodoc.call, autodoc.run, ed:run), else the user, whose input is the only other
// thing that starts work on the loop.
func (h *Host) authorityNow() authority {
	if h.invoking != nil {
		return *h.invoking
	}
	if s := h.scripting; s != nil && s.eng != nil {
		if name, grants, ok := s.eng.Caller(); ok {
			return authority{script: name, grants: grants, ws: h.ws, verbs: s.verbs, left: h.wsLeft}
		}
	}
	return userAuthority()
}

// actingAs runs fn as a's invocation: a gated script's key or row running its command line.
func (h *Host) actingAs(a authority, fn func() error) error {
	if a.verbs == nil && h.scripting != nil {
		a.verbs = h.scripting.verbs
	}
	if a.left == nil {
		a.left = h.wsLeft
	}
	prev := h.invoking
	h.invoking = &a
	defer func() { h.invoking = prev }()
	return fn()
}

// bounds is a gated authority's bounds for a call, as the daemon takes them (rpc.BoundsProtocol):
// any served workspace for a read, its workspace for anything else; the TUI's config folder, which
// the daemon cannot know, protected.
func (h *Host) bounds(a authority, read bool) map[string]any {
	within := a.ws
	if read {
		within = "*"
	}
	m := map[string]any{"within": within}
	if h.cfgDir != "" {
		m["protect"] = []any{h.cfgDir}
	}
	return m
}

type authorityKey struct{}

// withAuthority is ctx carrying a, for h.call to bound the calls made under it.
func withAuthority(ctx context.Context, a authority) context.Context {
	return context.WithValue(ctx, authorityKey{}, a)
}

// authorityOf is the authority ctx carries; ok is false for the host's own work, which carries none.
func authorityOf(ctx context.Context) (authority, bool) {
	a, ok := ctx.Value(authorityKey{}).(authority)
	return a, ok
}

// boundedParams is params for a gated a's call of method: cut to, or padded to, the verb's own
// params, then a's bounds, as the gate sends a script's own calls (scripting.call).
func (h *Host) boundedParams(a authority, method string, params []any) ([]any, error) {
	v, ok := a.verbs[method]
	if !ok {
		return nil, fmt.Errorf("%s: the daemon's contracts are not known, so a script's work cannot carry its bounds", method)
	}
	return v.bounded(params, h.bounds(a, v.effect == "read")), nil
}
