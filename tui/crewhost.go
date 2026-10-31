package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// THE WINDOW'S CREW IDENTITY (docs/decisions/crew.md#scopes-and-fenced-ownership).
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
	Tr(id string) string
	Trf(id string, args ...any) string
	// DaemonAddress and StoreID are the daemon a session context names: its socket and its store
	DaemonAddress() string
	StoreID() string
	AskWhenFree(q Question)
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
	offered  bool                   // the orphans were offered this run (creworphans.go)
	sessions map[string]crewSession // the agents' sessions registered and not yet ended, by id
	// pending is each codename's registration whose answer was lost: the daemon may have committed
	// it, so its exact tuple is replayed, by the codename's next start or by settle, never a fresh
	// one that would find the session it left as busy. owed is each ended session whose crew.exit
	// the daemon has not confirmed, by id: asked again until it answers.
	pending map[string]*crewRegistration
	owed    map[string]crewOwedExit
}

// crewRegistration is one registration's tuple, everything crew.register is sent: a replay sends it
// as it was, which the daemon answers with the session it committed
// (docs/decisions/crew.md#sessions-and-the-guard).
type crewRegistration struct {
	codename, kind string
	prompts        bool // the CLI's settings report its permission prompts (crew.register's prompts)
	workspace      int64
	instance       string
	secret         string // until the context is written, or the registration is dropped
	scope          string
	epoch          int64
	store, socket  string // the daemon that answered it: the context names this one, never a later
	dir            string
	inflight       bool // sent, its answer not yet back
}

// crewOwedExit is a session ended here whose crew.exit the daemon has not confirmed.
type crewOwedExit struct {
	s        crewSession
	why      string
	inflight bool
}

// crewSession is one agent's session, as registered under the window's scope.
type crewSession struct {
	ID, Profile, Scope string
	Epoch              int64
	Dir                string            // the folder whose context file names it
	reg                *crewRegistration // what registered it: its secret, kind and daemon, for its context
}

// errNoCrewScope is a registration asked of a window that holds no scope.
var errNoCrewScope = errors.New("this window holds no crew scope")

func newCrewHost(c crewCore, agentDir string) *crewHost {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand: never fails on the platforms AutoDoc runs on
	proc, _ := crew.ProcRef(os.Getpid())
	return &crewHost{c: c, dir: filepath.Join(agentBase(agentDir), ".crew-scopes"), run: hex.EncodeToString(b), proc: proc,
		sessions: map[string]crewSession{}, pending: map[string]*crewRegistration{}, owed: map[string]crewOwedExit{}}
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
	none  bool  // the daemon has no crew (one before protocol 37): the window simply has none, said nowhere
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
		if !ok || r.none {
			return // panicked (kept and said where it happened, work.go), or a daemon with no crew
		}
		if r.err != nil {
			h.c.Notify(h.c.Trf("autodoc.crew.no_scope", r.err.Error()))
			h.c.After(crewRenewEvery, h.claim) // a store or daemon failure may pass: try again
			return
		}
		if r.warn != nil {
			h.c.Notify(h.c.Trf("autodoc.crew.scope_unlisted", r.warn.Error()))
		}
		if held != "" && r.scope != held {
			h.c.Notify(h.c.Trf("autodoc.crew.scope_lost", held, r.scope))
		}
		h.scope, h.epoch = r.scope, r.epoch
		h.gen++
		h.renewLater(h.gen)
		h.settle()
		h.offerOrphans()
	})
}

// claimOff reattaches held when it is set, else claims a listed scope or a new one. Off the loop.
func (h *crewHost) claimOff(ctx context.Context, held string) claimed {
	r := h.claimAny(ctx, held)
	if code(r.err) == golibrpc.CodeMethodNotFound {
		return claimed{none: true} // the next connection, perhaps to a newer daemon, asks again
	}
	return r
}

// claimAny is claimOff's claim: held reattached, else a listed scope or a new one.
func (h *crewHost) claimAny(ctx context.Context, held string) claimed {
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
			h.settle() // the daemon answers again: what the window owes it is asked again
			h.renewLater(gen)
		})
	})
}

// Stopped releases the scope on a clean quit, so the run can't reattach and its agents' sessions end.
// The loop has ended, so nothing is shown: a release that fails leaves the lease to lapse in 90 s.
// The release ends every session, so no registration or exit is owed any longer.
func (h *crewHost) Stopped() {
	scope, epoch, ok := h.held()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), crewReleaseWait)
	defer cancel()
	_, _ = h.c.Call(ctx, "crew.release", scope, h.run, epoch)
	h.scope = ""
	for id, s := range h.sessions { // released: every session has ended, and its credential with it
		_ = crew.RemoveContext(s.Dir, s.ID)
		delete(h.sessions, id)
	}
	clear(h.pending)
	clear(h.owed)
}

var (
	// errCrewStale is a registration answered once the window no longer held its scope and epoch,
	// or was connected to another daemon or store: its session is ended, never started.
	errCrewStale = errors.New("the window's crew scope or daemon changed while the session was registered")
	// errCrewResolving is a start while the codename's lost registration is being asked again.
	errCrewResolving = errors.New("its last registration's answer was lost and is being asked again; start it again in a moment")
)

// crewRefused reports whether err is the daemon's definite refusal of a crew verb: nothing took
// effect, and sending it again changes nothing. Any other failure (a lost connection, a timeout, a
// store failure) may have committed, or may pass, so the verb stays owed.
func crewRefused(err error) bool {
	switch code(err) {
	case crew.CodeScopeBusy, crew.CodeStaleEpoch, crew.CodeCodenameBusy, crew.CodeInstanceReused, crew.CodeBadCredential,
		golibrpc.CodeMethodNotFound, golibrpc.CodeInvalidParams:
		return true
	}
	return false
}

func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// register starts codename's session under the window's scope
// (docs/decisions/crew.md#sessions-and-the-guard, docs/decisions/crew.md#the-session-context), off
// the loop; then is told on it. Nothing is written here: the caller, once it knows the session is
// still wanted, publishes its context and starts its process, or ends it. A registration whose
// answer was lost is kept, since the daemon may have committed it, and replayed as it was by the
// codename's next start (or by settle, which ends it); one the daemon refused is dropped. A lost
// one with other arguments (its profile edited since) is ended first. workspace is the workspace's
// id in the store; instance the host's spawn id.
func (h *crewHost) register(codename, kind string, prompts bool, workspace int64, instance, dir string, then func(crewSession, error)) {
	scope, epoch, ok := h.held()
	if !ok {
		then(crewSession{}, errNoCrewScope)
		return
	}
	store, socket := h.c.StoreID(), h.c.DaemonAddress()
	r := h.pending[codename]
	if r != nil && r.inflight {
		then(crewSession{}, errCrewResolving)
		return
	}
	if r != nil && (r.scope != scope || r.epoch != epoch || r.store != store) {
		delete(h.pending, codename) // the claim that changed the epoch ended its session; another store has none
		r = nil
	}
	if r != nil && (r.kind != kind || r.prompts != prompts || r.workspace != workspace) {
		h.resolve(r)
		then(crewSession{}, errCrewResolving)
		return
	}
	if r == nil {
		r = &crewRegistration{codename: codename, kind: kind, prompts: prompts, workspace: workspace, instance: instance, secret: newSecret(),
			scope: scope, epoch: epoch, store: store}
		h.pending[codename] = r
	}
	r.socket, r.dir = socket, dir // a replay names the daemon that answers it
	r.inflight = true
	h.c.Go(func(ctx context.Context) any { return h.send(ctx, r) }, func(v any) {
		r.inflight = false
		then(h.registered(r, v))
	})
}

// send is r's crew.register, off the loop.
func (h *crewHost) send(ctx context.Context, r *crewRegistration) any {
	v, err := h.c.Call(ctx, "crew.register", r.scope, h.run, r.epoch, r.codename, r.kind, strconv.FormatInt(r.workspace, 10), r.instance, r.secret, r.prompts)
	if err != nil {
		return err
	}
	m, _ := v.(map[string]any)
	return crewSession{ID: str(m, "session"), Profile: str(m, "profile"), Scope: r.scope, Epoch: r.epoch, Dir: r.dir, reg: r}
}

// registered is r's answer v, on the loop: a session, now counted live, or why there is none. r
// stays pending only when the answer was lost.
func (h *crewHost) registered(r *crewRegistration, v any) (crewSession, error) {
	s, ok := v.(crewSession)
	if !ok {
		err, _ := v.(error)
		if err == nil {
			err = errors.New("the registration did not finish")
		}
		if crewRefused(err) {
			h.forget(r)
		}
		return crewSession{}, err
	}
	h.forget(r)
	h.sessions[s.ID] = s
	return s, nil
}

func (h *crewHost) forget(r *crewRegistration) {
	if h.pending[r.codename] == r {
		delete(h.pending, r.codename)
	}
}

// resolve ends lost registration r, which nobody is starting: replayed as it was, the session the
// daemon committed comes back and is ended (start_failed), so its codename is free.
func (h *crewHost) resolve(r *crewRegistration) {
	if r.inflight {
		return
	}
	r.inflight = true
	h.c.Go(func(ctx context.Context) any { return h.send(ctx, r) }, func(v any) {
		r.inflight = false
		if s, err := h.registered(r, v); err == nil {
			h.exit(s, crew.EndedStartFailed)
		}
	})
}

// publish writes s's context file in its agent folder (0600, through a rename) for the process about
// to start, and answers its path, once s is still this window's: registered here and not ended,
// under the scope and epoch the window holds, on the daemon and store it was registered with. The
// context names that daemon, and its credential the registration's secret, which is then dropped. A
// session no longer the window's is refused (errCrewStale), and the caller ends it.
func (h *crewHost) publish(s crewSession) (string, error) {
	r := s.reg
	if _, live := h.sessions[s.ID]; !live || r == nil || r.secret == "" || s.Scope != h.scope || s.Epoch != h.epoch ||
		r.store != h.c.StoreID() || r.socket != h.c.DaemonAddress() {
		return "", errCrewStale
	}
	path, err := crew.WriteContext(s.Dir, crew.Context{Store: r.store, Socket: r.socket, Scope: s.Scope, Profile: s.Profile,
		Session: s.ID, Credential: crew.Credential(s.ID, r.secret), Kind: r.kind})
	r.secret = "" // kept no longer than it is needed
	return path, err
}

// exit ends s, why: its context file is removed at once on the loop, so no later process reads its
// credential; it is s's own file, so a newer session's in the folder stays. The daemon is told off
// the loop; until it answers, the exit is owed, and settle asks again.
func (h *crewHost) exit(s crewSession, why string) {
	_ = crew.RemoveContext(s.Dir, s.ID)
	delete(h.sessions, s.ID)
	h.owed[s.ID] = crewOwedExit{s: s, why: why}
	h.sendExit(s.ID)
}

func (h *crewHost) sendExit(id string) {
	o, ok := h.owed[id]
	if !ok || o.inflight {
		return
	}
	o.inflight = true
	h.owed[id] = o
	h.c.Go(func(ctx context.Context) any {
		_, err := h.c.Call(ctx, "crew.exit", o.s.Scope, h.run, o.s.Epoch, o.s.ID, o.why)
		return err
	}, func(v any) {
		o, ok := h.owed[id]
		if !ok {
			return
		}
		if err, _ := v.(error); v == nil || crewRefused(err) { // ended, or ended already by the scope changing hands
			delete(h.owed, id)
			return
		}
		o.inflight = false
		h.owed[id] = o
	})
}

// settle asks the daemon again for what the window owes it, each time it answers a renewal or a
// claim: every exit not yet confirmed, and every lost registration nobody is starting, replayed and
// ended. One under a scope or epoch the window no longer holds was ended by the claim that changed it.
func (h *crewHost) settle() {
	for id, o := range h.owed {
		if o.s.Scope != h.scope || o.s.Epoch != h.epoch {
			delete(h.owed, id)
			continue
		}
		h.sendExit(id)
	}
	for codename, r := range h.pending {
		if r.scope != h.scope || r.epoch != h.epoch {
			if !r.inflight {
				delete(h.pending, codename)
			}
			continue
		}
		h.resolve(r)
	}
}
