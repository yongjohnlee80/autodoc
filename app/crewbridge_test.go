//go:build unix

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// fakeBridge is an agyBridge whose daemon and language server the cell plays: instance answers the
// daemon's instance (or err), dials the language server's connections in turn, and calls records
// each call by method.
type fakeBridge struct {
	*agyBridge
	mu      sync.Mutex // the bridge's goroutine and the cell's both touch what follows
	inst    string
	instErr error
	dials   []error // consumed one per look; nil when empty
	refuse  string  // a method the daemon answers with a credential refusal
	calls   []string
	params  map[string][]any
}

func newFakeBridge() *fakeBridge {
	f := &fakeBridge{inst: "instance-1", params: map[string][]any{}}
	f.agyBridge = &agyBridge{
		c:       sessionContext{Session: "s1", Credential: "s1.secret", Store: "store-1", Kind: "agy"},
		locator: `{"address":"127.0.0.1:4000","conversation":"c-1"}`, token: "tok", addr: "127.0.0.1:4000",
		bound: bridgeLook,
	}
	f.call = func(_ context.Context, method string, params ...any) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, method)
		f.params[method] = params
		if method == f.refuse {
			return nil, &golibrpc.Error{Code: crew.CodeBadCredential, Message: "refused"}
		}
		return map[string]any{}, nil
	}
	f.instance = func(context.Context) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.inst, f.instErr
	}
	f.dial = func(context.Context) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.dials) == 0 {
			return nil
		}
		err := f.dials[0]
		f.dials = f.dials[1:]
		return err
	}
	f.hosts = func(context.Context) error { return nil }
	return f
}

func (f *fakeBridge) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

// runFor runs f's bridge for ticks looks after its first, and answers its error, or nil when it was
// still running. Each look is held at its first call (the daemon's instance) until the one before
// it has ended and between has run (between(i): after look i, before look i+1), so each look sees
// exactly the state the cell set for it; the last look runs to its end before the bridge is stopped.
func runFor(t *testing.T, f *fakeBridge, ticks int, between func(i int)) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	step := make(chan struct{})
	instance := f.instance
	f.instance = func(c context.Context) (string, error) {
		select {
		case <-step:
		case <-c.Done():
			return "", c.Err()
		}
		return instance(c)
	}
	done := make(chan error, 1)
	go func() { done <- f.run(ctx, tick, &bytes.Buffer{}) }()
	for i := 0; i <= ticks; i++ {
		if i > 0 {
			// taken only once look i-1 has ended: look i starts, and waits on step
			select {
			case tick <- time.Now():
			case err := <-done:
				return err
			}
			if between != nil {
				between(i - 1)
			}
		}
		select {
		case step <- struct{}{}:
		case err := <-done:
			return err
		}
	}
	select { // taken once the last look has ended; the look it starts ends with ctx
	case tick <- time.Now():
	case err := <-done:
		return err
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// TestTheBridgeRegistersOnceAndTicksReadOnly: the bridge registers its endpoint, with the token,
// at its start, and its looks after (ten here) only ask whoami: a registration would raise the
// generation and reset the session's wake state, so a tick never makes one.
func TestTheBridgeRegistersOnceAndTicksReadOnly(t *testing.T) {
	t.Parallel()
	f := newFakeBridge()
	if err := runFor(t, f, 10, nil); err != nil {
		t.Fatal(err)
	}
	if n := f.count("crew.endpoint"); n != 1 {
		t.Errorf("%d registrations over ten looks, want the first alone", n)
	}
	if n := f.count("crew.whoami"); n != 10 {
		t.Errorf("%d whoami calls over ten looks, want ten", n)
	}
	p := f.params["crew.endpoint"]
	if len(p) != 4 || p[0] != "s1.secret" || p[1] != "agy" || p[2] != f.locator || p[3] != "tok" {
		t.Errorf("the registration's params %v, want the credential, agy, the locator and the token", p)
	}
}

// TestTheBridgeRegistersAgainForANewDaemon: a daemon of another instance (a restart, which forgets
// the token) is registered with again, whether a look saw it down between or not; a daemon that does
// not answer is tried again, not left.
func TestTheBridgeRegistersAgainForANewDaemon(t *testing.T) {
	t.Parallel()
	f := newFakeBridge()
	if err := runFor(t, f, 6, func(i int) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch i {
		case 1:
			f.instErr = errors.New("connection refused")
		case 2:
			f.instErr, f.inst = nil, "instance-2"
		case 4:
			f.inst = "instance-3"
		}
	}); err != nil {
		t.Fatal(err)
	}
	if n := f.count("crew.endpoint"); n != 3 {
		t.Errorf("%d registrations, want one at the start and one for each new daemon", n)
	}
}

// TestTheBridgeEndsWithItsSession: the bridge stops when the daemon refuses its credential, at its
// registration or at a look (the session ended, or its folder now holds another's): it never reads
// another context, so it never registers another session.
func TestTheBridgeEndsWithItsSession(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"crew.endpoint", "crew.whoami"} {
		f := newFakeBridge()
		f.refuse = method
		err := runFor(t, f, 3, nil)
		if err == nil || !strings.Contains(err.Error(), "credential is refused") {
			t.Errorf("a refused %s: %v, want the bridge stopped", method, err)
		}
		if n := f.count("crew.endpoint"); n > 1 {
			t.Errorf("a refused %s: %d registrations", method, n)
		}
	}
}

// TestTheBridgeEndsWithItsLanguageServer: the bridge stops when the language server refuses
// bridgeRefusals looks in a row, not on fewer, and not when it answers but does not confirm the
// conversation (a busy or restarting server).
func TestTheBridgeEndsWithItsLanguageServer(t *testing.T) {
	t.Parallel()
	refused := syscall.ECONNREFUSED
	f := newFakeBridge()
	f.dials = []error{refused, refused, nil, refused, refused}
	f.hosts = func(context.Context) error { return errors.New("not confirmed") }
	if err := runFor(t, f, 5, nil); err != nil {
		t.Errorf("two refusals, an answer, two more: %v, want it still running", err)
	}
	f = newFakeBridge()
	f.dials = []error{refused, refused, refused}
	if err := runFor(t, f, 5, nil); err == nil || !strings.Contains(err.Error(), "refused 3 looks in a row") {
		t.Errorf("three refusals in a row: %v, want the bridge stopped", err)
	}
}

// TestTheBridgeLockIsItsSessions: a session's bridge lock is held by one bridge at a time; another
// session's bridge in the same folder takes its own; a lock its holder let go of (or died holding:
// the kernel drops it) is taken again.
func TestTheBridgeLockIsItsSessions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	unlock, err := lockBridge(dir, "agy", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockBridge(dir, "agy", "s1"); !errors.Is(err, errBridgeRunning) {
		t.Errorf("a second bridge for s1: %v, want errBridgeRunning", err)
	}
	other, err := lockBridge(dir, "agy", "s2")
	if err != nil {
		t.Errorf("s2's bridge beside s1's: %v", err)
	} else {
		other()
	}
	unlock()
	again, err := lockBridge(dir, "agy", "s1")
	if err != nil {
		t.Errorf("s1's lock after its holder let go: %v", err)
	} else {
		again()
	}
}

// TestTheBridgesAddressIsLoopback: agy's address is made a loopback IP (localhost is 127.0.0.1,
// never resolved), and any other host is refused.
func TestTheBridgesAddressIsLoopback(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"localhost:4000": "127.0.0.1:4000", "127.0.0.1:4000": "127.0.0.1:4000",
		"[::1]:4000": "[::1]:4000", "http://localhost:4000": "127.0.0.1:4000"} {
		if got, err := loopbackAddress(in); err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"10.0.0.1:4000", "example.com:4000", "4000", "localhost"} {
		if got, err := loopbackAddress(bad); err == nil {
			t.Errorf("%s: %q, want refused", bad, got)
		}
	}
	if _, err := newAgyBridge(sessionContext{Kind: "agy"}, func(string) string { return "" }); err == nil {
		t.Error("a bridge with no language server in its environment was made")
	}
	if _, err := newAgyBridge(sessionContext{Kind: "claude"}, func(string) string { return "x" }); err == nil {
		t.Error("a bridge for a claude session was made")
	}
}

// TestTheBridgesFilesAreBesideTheContexts: the bridge's lock and log are in the agent folder that
// holds the context file $AUTODOC_CREW_CONTEXT names, never in the contexts' folder, where each
// would be taken for another session's context: with the bridge's lock held, the agent folder
// still has its one context.
func TestTheBridgesFilesAreBesideTheContexts(t *testing.T) {
	t.Parallel()
	agent := t.TempDir()
	path, err := crew.WriteContext(agent, crew.Context{Store: "store-1", Socket: "/x.sock", Session: "s1",
		Credential: "s1.secret", Kind: "agy"})
	if err != nil {
		t.Fatal(err)
	}
	if got := bridgeDir(path, "/elsewhere"); got != agent {
		t.Errorf("the bridge's folder for %s is %s, want the agent folder %s", path, got, agent)
	}
	if got := bridgeDir("", "/here"); got != "/here" {
		t.Errorf("with no $AUTODOC_CREW_CONTEXT the bridge's folder is %s, want the working directory", got)
	}
	unlock, err := lockBridge(bridgeDir(path, "/elsewhere"), "agy", "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if c, err := findSessionContext("", agent); err != nil || c.Session != "s1" {
		t.Errorf("the agent folder's context with the bridge's lock held: %+v %v, want s1's alone", c, err)
	}
}

// unanswered is a call that gets no answer: it ends with ctx, or fails after 2 s when ctx has no
// bound, so a missing bound fails the cell rather than hanging it.
func unanswered(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
		return errors.New("an unanswered call was not bounded")
	}
}

// TestABridgeLookThatGetsNoAnswerEnds: a language server that takes the connection and never
// answers the conversation check, then a daemon that never answers, each hold one look for its
// bound and no longer. The looks after still come: the session's end is seen and the bridge stops.
// The unanswered daemon's look leaves the registration standing (the same instance answers after,
// so nothing registers again).
func TestABridgeLookThatGetsNoAnswerEnds(t *testing.T) {
	t.Parallel()
	f := newFakeBridge()
	f.bound = 200 * time.Millisecond
	var instHang bool
	var hostsDeadlines int
	f.hosts = func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); ok {
			f.mu.Lock()
			hostsDeadlines++
			f.mu.Unlock()
		}
		return unanswered(ctx)
	}
	inner := f.instance
	f.instance = func(ctx context.Context) (string, error) {
		f.mu.Lock()
		hang := instHang
		f.mu.Unlock()
		if hang {
			return "", unanswered(ctx)
		}
		return inner(ctx)
	}
	err := runFor(t, f, 4, func(i int) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch i {
		case 0:
			instHang = true
		case 1:
			instHang = false
		case 2:
			f.refuse = "crew.whoami"
		}
	})
	if err == nil || !strings.Contains(err.Error(), "credential is refused") {
		t.Fatalf("after unanswered looks: %v, want the session's end seen and the bridge stopped", err)
	}
	if n := f.count("crew.endpoint"); n != 1 {
		t.Errorf("%d registrations, want the first alone: a look the daemon did not answer is no new daemon", n)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if hostsDeadlines == 0 {
		t.Error("the conversation check ran with no deadline")
	}
}

// TestTheDetachedBridgeIsPinnedToItsSession: the detached bridge reads the context the bridge that
// started it read, and no other. Here the working directory's context (no $AUTODOC_CREW_CONTEXT)
// is A's when the parent reads it and B's (A ended, B started in the same agent folder) when the
// child starts, which refuses rather than take up B with A's language server. Its command line
// carries no credential.
func TestTheDetachedBridgeIsPinnedToItsSession(t *testing.T) {
	t.Parallel()
	agent := t.TempDir()
	ctxOf := func(s string) crew.Context {
		return crew.Context{Store: "store-1", Socket: "/x.sock", Session: s, Credential: s + ".secret", Kind: "agy"}
	}
	pathA, err := crew.WriteContext(agent, ctxOf("sa"))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := pinnedContext("", agent, "")
	if err != nil || parent.Session != "sa" {
		t.Fatalf("the parent's context: %+v %v", parent, err)
	}
	argv, env := bridgeChild("/bin/autodoc", contextFile("", agent, parent), parent.Session, []string{"--agy"},
		[]string{"AUTODOC_CREW_CONTEXT=/elsewhere", "ANTIGRAVITY_CSRF_TOKEN=tok"})
	child := func() (string, string) {
		envFile, session := "", ""
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "AUTODOC_CREW_CONTEXT="); ok {
				envFile = v
			}
		}
		for i, a := range argv {
			if a == "--session" && i+1 < len(argv) {
				session = argv[i+1]
			}
		}
		return envFile, session
	}
	envFile, session := child()
	if envFile != pathA || session != "sa" {
		t.Fatalf("the child is pinned to %q and %q, want A's file %q and sa", envFile, session, pathA)
	}
	if c, err := pinnedContext(envFile, agent, session); err != nil || c.Session != "sa" {
		t.Errorf("the child, A still there: %+v %v, want A's", c, err)
	}
	for _, a := range argv {
		if strings.Contains(a, "secret") || strings.Contains(a, "tok") {
			t.Errorf("the child's command line carries %q", a)
		}
	}
	if !slices.Contains(env, "ANTIGRAVITY_CSRF_TOKEN=tok") {
		t.Error("the child's environment lost the language server's token")
	}
	if err := crew.RemoveContext(agent, "sa"); err != nil {
		t.Fatal(err)
	}
	if _, err := crew.WriteContext(agent, ctxOf("sb")); err != nil {
		t.Fatal(err)
	}
	if c, err := pinnedContext("", agent, ""); err != nil || c.Session != "sb" {
		t.Fatalf("the folder's one context is now %+v %v, want B's (what an unpinned child would take up)", c, err)
	}
	if c, err := pinnedContext(envFile, agent, session); err == nil {
		t.Errorf("the child, A replaced by B: took up %s, want refused", c.Session)
	}
	// the session a bridge was started for decides: started for sb, it refuses sa's context
	if _, err := crew.WriteContext(agent, ctxOf("sa")); err != nil {
		t.Fatal(err)
	}
	if err := crew.RemoveContext(agent, "sb"); err != nil {
		t.Fatal(err)
	}
	if c, err := pinnedContext(envFile, agent, "sb"); err == nil {
		t.Errorf("a bridge started for sb took up %s", c.Session)
	}
}

// TestTheBridgeReportsTheAgentsPermissionPrompts: at each prompt tick the bridge reads the
// conversation's prompt from the offset its last read answered; its first look clears what an
// earlier bridge may have raised; a prompt showing raises the session's permission attention once,
// saying what it asks, again when another takes its place; a failed read changes nothing (logged
// once); none showing clears what it raised, once; and each look that read says it is alive.
func TestTheBridgeReportsTheAgentsPermissionPrompts(t *testing.T) {
	f := newFakeBridge()
	type read struct {
		p    crew.AgyPrompt
		next int
		err  error
	}
	script := []read{
		{crew.AgyPrompt{}, 4, nil},
		{crew.AgyPrompt{Showing: true, Asks: "command: touch x"}, 5, nil},
		{crew.AgyPrompt{Showing: true, Asks: "command: touch x"}, 5, nil}, // the same prompt: raised already
		{crew.AgyPrompt{}, 5, errors.New("busy")},                         // a failed read: nothing changes
		{crew.AgyPrompt{}, 5, errors.New("busy")},
		{crew.AgyPrompt{Showing: true, Asks: "command: rm y"}, 6, nil}, // another took its place
		{crew.AgyPrompt{}, 7, nil},                                     // answered: cleared
		{crew.AgyPrompt{}, 7, nil},                                     // still none: nothing
	}
	var mu sync.Mutex
	var offsets []string
	var attention []string
	i := 0
	f.prompt = func(_ context.Context, offset int) (crew.AgyPrompt, int, error) {
		mu.Lock()
		defer mu.Unlock()
		offsets = append(offsets, strconv.Itoa(offset))
		r := script[i]
		i++
		return r.p, r.next, r.err
	}
	call := f.call
	f.call = func(ctx context.Context, method string, params ...any) (any, error) {
		if method == "crew.attention" {
			mu.Lock()
			attention = append(attention, fmt.Sprint(params[1:]...))
			mu.Unlock()
		}
		return call(ctx, method, params...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	var log bytes.Buffer
	done := make(chan struct{})
	go func() { defer close(done); f.watchPrompts(ctx, tick, &log) }()
	for range script {
		tick <- time.Now()
	}
	cancel() // the last look runs to its end: its stand-ins don't heed ctx
	<-done
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(offsets, ","); got != "0,4,5,5,5,5,6,7" {
		t.Fatalf("the reads' offsets %s, want each the last read's answer", got)
	}
	// the first look clears what an earlier bridge of the session may have raised; each look that
	// read the language server says the reporter is alive, and a failed one doesn't
	var told []string
	alive := 0
	for _, a := range attention {
		if a == "alive" {
			alive++
			continue
		}
		told = append(told, a)
	}
	want := []string{"clear", "raise" + crew.AttentionPermission + "command: touch x", "raise" + crew.AttentionPermission + "command: rm y", "clear"}
	if strings.Join(told, "|") != strings.Join(want, "|") {
		t.Fatalf("the attention calls %q, want %q", told, want)
	}
	if alive != len(script)-2 {
		t.Fatalf("%d heartbeats, want one for each of the %d looks that read the server", alive, len(script)-2)
	}
	if n := strings.Count(log.String(), "looking for the agent's permission prompt"); n != 1 {
		t.Fatalf("two failed reads in a row logged %d times, want once:\n%s", n, log.String())
	}
}

// launchTurn gives f a launch turn the cell plays: the running look answers turns[i] at its i-th
// call (the last for every call after), each recorded in f.calls as "turn", before or after the
// registration; sleep advances a fake clock by its d and records "sleep"; turnBound is bound.
func (f *fakeBridge) launchTurn(bound time.Duration, turns ...any) {
	now := time.Unix(1_800_000_000, 0)
	n := 0
	f.turnEvery, f.turnBound = 2*time.Second, bound
	f.now = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return now }
	f.sleep = func(_ context.Context, d time.Duration) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, "sleep")
		now = now.Add(d)
		return true
	}
	f.running = func(context.Context) (bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, "turn")
		v := turns[min(n, len(turns)-1)]
		n++
		if err, ok := v.(error); ok {
			return false, err
		}
		return v.(bool), nil
	}
}

// before is the calls f made before its first call of method.
func (f *fakeBridge) before(method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.Index(f.calls, method)
	if i < 0 {
		return slices.Clone(f.calls)
	}
	return slices.Clone(f.calls[:i])
}

// TestTheBridgesFirstRegistrationWaitsForTheLaunchTurn
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): a bridge started inside the agent's launch
// turn registers only once that turn has ended, so the first wake starts a turn of its own, where
// agy has given the agent its MCP tools; a turn already ended registers at once.
func TestTheBridgesFirstRegistrationWaitsForTheLaunchTurn(t *testing.T) {
	t.Parallel()
	f := newFakeBridge()
	f.launchTurn(2*time.Minute, true, true, true, false)
	if err := runFor(t, f, 0, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := f.before("crew.endpoint"), []string{"turn", "sleep", "turn", "sleep", "turn", "sleep", "turn"}; !slices.Equal(got, want) {
		t.Fatalf("before the registration: %v, want the running turn looked at until it ended, %v", got, want)
	}
	if f.count("crew.endpoint") != 1 {
		t.Fatalf("%d registrations, want one once the turn ended", f.count("crew.endpoint"))
	}
	ended := newFakeBridge()
	ended.launchTurn(2*time.Minute, false)
	if err := runFor(t, ended, 0, nil); err != nil {
		t.Fatal(err)
	}
	if got := ended.before("crew.endpoint"); !slices.Equal(got, []string{"turn"}) || ended.count("crew.endpoint") != 1 {
		t.Fatalf("a launch turn already ended: %v before the registration, want one look and no wait", got)
	}
}

// TestTheBridgeRegistersAnywayAtItsBound: a launch turn that never ends, or a running state the
// bridge can't read, never keeps mail from the agent: at turnBound it registers anyway, and an
// unreadable state registers at once. Only the first registration waits: a new daemon is registered
// with at its look, whatever the turn.
func TestTheBridgeRegistersAnywayAtItsBound(t *testing.T) {
	t.Parallel()
	f := newFakeBridge()
	f.launchTurn(10*time.Second, true)
	if err := runFor(t, f, 2, func(i int) {
		if i == 0 {
			f.mu.Lock()
			f.inst = "instance-2" // the daemon restarted
			f.mu.Unlock()
		}
	}); err != nil {
		t.Fatal(err)
	}
	before := f.before("crew.endpoint")
	if n := strings.Count(strings.Join(before, " "), "sleep"); n != 5 {
		t.Fatalf("before the registration: %v; want it to wait out its 10 s bound (5 looks of 2 s), then register", before)
	}
	if n := f.count("crew.endpoint"); n != 2 {
		t.Fatalf("%d registrations; want the first at the bound, and the new daemon's at its look", n)
	}
	if n := f.count("turn"); n != 6 {
		t.Fatalf("%d looks at the turn; want the first registration's alone (a re-registration doesn't wait)", n)
	}
	broken := newFakeBridge()
	broken.launchTurn(2*time.Minute, errors.New("the language server said 500"))
	if err := runFor(t, broken, 0, nil); err != nil {
		t.Fatal(err)
	}
	if got := broken.before("crew.endpoint"); !slices.Equal(got, []string{"turn"}) {
		t.Fatalf("an unreadable turn: %v before the registration, want it registered at once", got)
	}
}

// TestTheBridgeEndsWithinALookOfItsSessionsEnd: the bridge's every-2-s look (watchPrompts) says
// the reporter is alive with the session's credential; once the daemon refuses it (the window ended
// the session: the agent's tab closed), that look stops the bridge, within a few ticks rather than at
// the next 30-s look. A bridge that outlived its session kept answering wakes for an agent no longer
// there (a re-parented orphan, cloud-strife's finding, 2026-11-01).
func TestTheBridgeEndsWithinALookOfItsSessionsEnd(t *testing.T) {
	f := newFakeBridge()
	f.prompt = func(context.Context, int) (crew.AgyPrompt, int, error) { return crew.AgyPrompt{}, 0, nil }
	f.refuse = "crew.attention"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); f.watchPrompts(ctx, tick, &bytes.Buffer{}) }()
	stopped := false
	for i := 0; i < 3 && !stopped; i++ {
		select {
		case tick <- time.Now():
		case <-done:
			stopped = true
		}
	}
	if !stopped {
		select {
		case <-done:
			stopped = true
		case <-time.After(2 * time.Second):
		}
	}
	if !stopped {
		t.Fatalf("the bridge's look kept running after the daemon refused its session's credential (calls %v)", f.calls)
	}
}
