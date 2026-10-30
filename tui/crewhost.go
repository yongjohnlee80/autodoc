package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// THE WINDOW'S CREW IDENTITY (ADR 1793271500 §4.3).
//
// An AutoDoc window hosts its agents under one scope, autodoc:<id>, which it claims from the daemon
// with a run token: random, made once per process, and kept in memory only. Holding the token is
// what lets this process reattach after a reconnect; another window, or this one's crash successor,
// can read the scope list but never reattach. The scope ids, not secrets, are kept in
// <agentDir>/.crew-scopes/, so a successor claims a crashed window's scope back once its lease has
// lapsed, before it would make a new one.

// crewRenewEvery is how often the window renews its scope's lease, a third of the daemon's 90 s.
const crewRenewEvery = 30 * time.Second

// crewReleaseWait bounds the release a clean quit sends: the program is stopping, and a daemon that
// doesn't answer must not hold it.
const crewReleaseWait = 2 * time.Second

// crewCore is what the window's crew identity needs of the program.
type crewCore interface {
	Call(ctx context.Context, verb string, params ...any) (any, error)
	Go(fn func(context.Context) any, then func(any))
	After(d time.Duration, fn func())
	Notify(msg string)
}

// crewHost holds the window's scope. Its fields are the loop's: work off it answers through Go.
type crewHost struct {
	c        crewCore
	dir      string // <agentDir>/.crew-scopes: the scope ids this machine's windows claimed
	run      string // this process's run token: in memory only, never written
	proc     string // this process's reference (crew.ProcRef), "" when unknown
	scope    string // the scope held, "" before the first claim
	epoch    int64
	gen      uint64 // bumped by each claim: an earlier claim's renewals stop
	claiming bool
}

func newCrewHost(c crewCore, agentDir string) *crewHost {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand: never fails on the platforms AutoDoc runs on
	proc, _ := crew.ProcRef(os.Getpid())
	return &crewHost{c: c, dir: filepath.Join(agentBase(agentDir), ".crew-scopes"), run: hex.EncodeToString(b), proc: proc}
}

func (*crewHost) Name() string                 { return "crew" }
func (*crewHost) State() map[string]any        { return map[string]any{} }
func (*crewHost) Commands() map[string]Command { return map[string]Command{} }

// held is the scope this window holds and its epoch; ok is false before a claim has succeeded.
func (h *crewHost) held() (scope string, epoch int64, ok bool) {
	return h.scope, h.epoch, h.scope != ""
}

// Connected claims the window's scope on the first connection, and reattaches on every later one:
// a new connection is a new client token, never a new owner.
func (h *crewHost) Connected() { h.claim() }

// claimed is a claim's answer.
type claimed struct {
	scope string
	epoch int64
	err   error // no scope: a daemon or store failure, shown, never a new identity minted silently
	warn  error // the scope is held, but its id could not be listed for a successor
}

func (h *crewHost) claim() {
	if h.claiming {
		return
	}
	h.claiming = true
	held := h.scope
	h.c.Go(func(ctx context.Context) any { return h.claimOff(ctx, held) }, func(v any) {
		h.claiming = false
		r, ok := v.(claimed)
		if !ok {
			return // the work panicked: kept and said where it happened (work.go)
		}
		if r.err != nil {
			h.c.Notify("This window's agents have no crew scope: " + r.err.Error())
			h.c.After(crewRenewEvery, h.claim) // a store or daemon failure may pass: try again
			return
		}
		if r.warn != nil {
			h.c.Notify("This window's crew scope is held, but not listed for a successor: " + r.warn.Error())
		}
		if held != "" && r.scope != held {
			h.c.Notify(fmt.Sprintf("This window lost its crew scope %s while away; its agents now live under %s.", held, r.scope))
		}
		h.scope, h.epoch = r.scope, r.epoch
		h.gen++
		h.renewLater(h.gen)
	})
}

// claimOff reattaches held when it is set, else claims a listed scope or a new one. Off the loop.
func (h *crewHost) claimOff(ctx context.Context, held string) claimed {
	if held != "" {
		epoch, err := h.claimOne(ctx, held)
		if err == nil {
			return claimed{scope: held, epoch: epoch}
		}
		if code(err) != crew.CodeScopeBusy {
			return claimed{err: err}
		}
		// another owner holds it now (this window was away past the lease): claim one afresh
	}
	return h.claimScope(ctx)
}

// claimScope claims the oldest listed scope that is free, or else a new one, which it lists. A busy
// scope is another live window's; any other failure ends the claim, so a daemon or store failure
// never mints a new identity.
func (h *crewHost) claimScope(ctx context.Context) claimed {
	for _, id := range h.listed() {
		epoch, err := h.claimOne(ctx, id)
		switch {
		case err == nil:
			return claimed{scope: id, epoch: epoch}
		case code(err) == crew.CodeScopeBusy:
			continue
		default:
			return claimed{err: err}
		}
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := "autodoc:" + hex.EncodeToString(b)
	epoch, err := h.claimOne(ctx, id)
	if err != nil {
		return claimed{err: err} // a lost answer is retried by the next connection with a new id: unused ids cost nothing
	}
	r := claimed{scope: id, epoch: epoch}
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		r.warn = err
	} else if err := writeAtomic(filepath.Join(h.dir, id), nil, 0o600); err != nil {
		r.warn = err
	}
	return r
}

func (h *crewHost) claimOne(ctx context.Context, scope string) (int64, error) {
	v, err := h.c.Call(ctx, "crew.claim", scope, h.run, h.proc)
	if err != nil {
		return 0, err
	}
	m, _ := v.(map[string]any)
	return num(m, "epoch"), nil
}

// listed is the scope ids in the folder, oldest first: a crashed window's scope comes back before a
// newer one's.
func (h *crewHost) listed() []string {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		return nil
	}
	type listedScope struct {
		id  string
		mod time.Time
	}
	var out []listedScope
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), "autodoc:") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, listedScope{e.Name(), info.ModTime()})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].mod.Before(out[j].mod) })
	ids := make([]string, len(out))
	for i, s := range out {
		ids[i] = s.id
	}
	return ids
}

// renewLater renews the scope's lease crewRenewEvery from now, while claim gen is the current one.
// A renewal refused for a stale epoch means another owner took the scope: the window claims again.
// Any other failure waits for the next renewal, or the reconnect that reattaches.
func (h *crewHost) renewLater(gen uint64) {
	h.c.After(crewRenewEvery, func() {
		if gen != h.gen || h.scope == "" {
			return
		}
		scope, epoch := h.scope, h.epoch
		h.c.Go(func(ctx context.Context) any {
			_, err := h.c.Call(ctx, "crew.renew", scope, h.run, epoch)
			return err
		}, func(v any) {
			if gen != h.gen {
				return
			}
			if err, _ := v.(error); err != nil && code(err) == crew.CodeStaleEpoch {
				h.claim()
				return
			}
			h.renewLater(gen)
		})
	})
}

// Stopped releases the scope on a clean quit, so the run can't reattach and its agents' sessions end.
// The loop has ended, so nothing is shown: a release that fails leaves the lease to lapse in 90 s.
func (h *crewHost) Stopped() {
	scope, epoch, ok := h.held()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), crewReleaseWait)
	defer cancel()
	_, _ = h.c.Call(ctx, "crew.release", scope, h.run, epoch)
	h.scope = ""
}
