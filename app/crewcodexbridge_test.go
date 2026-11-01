package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// codexStatusConn stands in for a connection to the app-server: each thread/read answers the next
// of statuses (past the list, the last), or fails when it is "fail"; every call is kept.
type codexStatusConn struct {
	mu       sync.Mutex
	statuses []string
	n        int
	calls    []string // "method threadId includeTurns"
	closed   int
}

func (c *codexStatusConn) call(_ context.Context, method string, params, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, _ := params.(map[string]any)
	c.calls = append(c.calls, fmt.Sprintf("%s %v %v", method, p["threadId"], p["includeTurns"]))
	s := c.statuses[min(c.n, len(c.statuses)-1)]
	c.n++
	switch s {
	case "fail":
		return errors.New("the read failed")
	case "not loaded": // the app-server's answer for a thread it has no longer
		return &codexRPCError{Method: method, Code: -32600, Message: "thread not loaded: " + fmt.Sprint(p["threadId"])}
	}
	return json.Unmarshal([]byte(`{"thread":{"status":`+s+`}}`), result)
}

func (c *codexStatusConn) close() { c.mu.Lock(); c.closed++; c.mu.Unlock() }

// daemonCalls keeps the bridge's calls to the daemon, each as "verb arg…" without the credential,
// and answers each with refuse (nil: none refused).
type daemonCalls struct {
	mu     sync.Mutex
	got    []string
	refuse func(call string) error
}

func (d *daemonCalls) call(_ context.Context, method string, params ...any) (any, error) {
	parts := []string{method}
	for _, p := range params[1:] {
		parts = append(parts, fmt.Sprint(p))
	}
	call := strings.Join(parts, " ")
	d.mu.Lock()
	d.got = append(d.got, call)
	d.mu.Unlock()
	if d.refuse != nil {
		return nil, d.refuse(call)
	}
	return nil, nil
}

func (d *daemonCalls) take() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	got := d.got
	d.got = nil
	return got
}

// testCodexBridge is a bridge of thread "t1" on conn, its daemon d; dials counts its dials.
func testCodexBridge(conn codexConn, d *daemonCalls, dials *int) *codexBridge {
	return &codexBridge{c: sessionContext{Credential: "cred", Kind: "codex"}, thread: "t1", bound: time.Second,
		server: func() (string, error) { return "/codex/sock", nil },
		dial: func(context.Context, string) (codexConn, error) {
			*dials++
			return conn, nil
		},
		call: d.call}
}

// looks runs n looks of b, in turn, on st (a fresh state when nil), failing the cell at an error;
// it answers the state.
func looks(t *testing.T, b *codexBridge, st *codexLookState, n int) *codexLookState {
	t.Helper()
	if st == nil {
		st = &codexLookState{unknown: true}
	}
	for range n {
		if err := b.look(context.Background(), st, io.Discard); err != nil {
			t.Fatalf("a look stopped the bridge: %v", err)
		}
	}
	return st
}

// TestTheCodexBridgeRaisesAndClearsByTheThreadsStatus (docs/decisions/crew.md#the-human-inbox): a
// thread waiting on an approval raises the session's permission attention, saying so; one waiting
// on the user's input raises it, saying that; the same prompt still showing raises nothing more;
// anything else clears it (and the first look clears what an earlier bridge may have raised). Every
// look that read the status says the reporter is alive, after its raise or clear; the connection
// is kept while reads succeed (the mutants: a flag not read; alive before the raise; the stale
// clear skipped).
func TestTheCodexBridgeRaisesAndClearsByTheThreadsStatus(t *testing.T) {
	conn := &codexStatusConn{}
	d, dials := &daemonCalls{}, 0
	b := testCodexBridge(conn, d, &dials)
	alive := "crew.attention alive"
	st := &codexLookState{unknown: true}
	for _, step := range []struct {
		name, status string
		want         []string
	}{
		{"idle, the first look", `{"type":"idle"}`, []string{"crew.attention clear", alive}},
		{"idle again", `{"type":"idle"}`, []string{alive}},
		{"active, waiting on nothing", `{"type":"active","activeFlags":[]}`, []string{alive}},
		{"waiting on an approval", `{"type":"active","activeFlags":["waitingOnApproval"]}`, []string{"crew.attention raise permission Codex waits on an approval", alive}},
		{"the same approval still", `{"type":"active","activeFlags":["waitingOnApproval"]}`, []string{alive}},
		{"waiting on the user's input", `{"type":"active","activeFlags":["waitingOnUserInput"]}`, []string{"crew.attention raise permission Codex waits on your input", alive}},
		{"idle once answered", `{"type":"idle"}`, []string{"crew.attention clear", alive}},
		{"not loaded", `{"type":"notLoaded"}`, []string{alive}},
	} {
		conn.mu.Lock()
		conn.statuses, conn.n = []string{step.status}, 0
		conn.mu.Unlock()
		looks(t, b, st, 1)
		if got := d.take(); !slices.Equal(got, step.want) {
			t.Errorf("%s: the daemon was told %q, want %q", step.name, got, step.want)
		}
	}
	if dials != 1 {
		t.Errorf("%d dials over looks that all read, want the one connection kept", dials)
	}
}

// TestTheCodexBridgeReadsItsOwnThreadsStatusAlone: every call the bridge makes on the app-server is
// thread/read of its own thread, metadata only: no resume, no subscription, no turns (the mutants:
// includeTurns true; another thread).
func TestTheCodexBridgeReadsItsOwnThreadsStatusAlone(t *testing.T) {
	conn := &codexStatusConn{statuses: []string{`{"type":"active","activeFlags":["waitingOnApproval"]}`}}
	d, dials := &daemonCalls{}, 0
	looks(t, testCodexBridge(conn, d, &dials), nil, 2)
	if len(conn.calls) != 2 {
		t.Errorf("%d calls on the app-server over two looks, want one each: %q", len(conn.calls), conn.calls)
	}
	for _, c := range conn.calls {
		if c != "thread/read t1 false" {
			t.Errorf("the bridge called %q on the app-server, want thread/read of t1 without its turns alone", c)
		}
	}
}

// TestACodexBridgeLookThatFailsSaysNothing: a look whose read fails raises nothing, clears nothing
// and says nothing alive (the session's prompts then read as unreported once its last heartbeat is
// 15 s old), and the next look dials again (the mutants: alive on a failed read; the connection
// kept after a failure).
func TestACodexBridgeLookThatFailsSaysNothing(t *testing.T) {
	conn := &codexStatusConn{statuses: []string{"fail", "fail", `{"type":"active","activeFlags":["waitingOnApproval"]}`}}
	d, dials := &daemonCalls{}, 0
	b := testCodexBridge(conn, d, &dials)
	st := looks(t, b, nil, 2)
	if got := d.take(); len(got) != 0 {
		t.Errorf("failed reads told the daemon %q, want nothing", got)
	}
	if dials != 2 || conn.closed != 2 || st.conn != nil {
		t.Errorf("%d dials, %d closes after two failed reads, want each failure's connection closed and dialed again", dials, conn.closed)
	}
	looks(t, b, st, 1)
	if got := d.take(); !slices.Equal(got, []string{"crew.attention raise permission Codex waits on an approval", "crew.attention alive"}) {
		t.Errorf("a read that succeeds after failures told the daemon %q, want the prompt raised and alive", got)
	}
}

// TestTheCodexBridgeNeverDialsAServerOutsideCodexsSocketFolder: a session whose server (its
// AUTODOC_CODEX_SERVER) resolves outside Codex's socket folder is refused: never dialed, nothing
// raised, nothing said alive, and never taken for gone, so the bridge goes on looking (the mutant:
// the bound skipped).
func TestTheCodexBridgeNeverDialsAServerOutsideCodexsSocketFolder(t *testing.T) {
	sockets, agent, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	withCodexSocketRoots(t, sockets)
	outside := filepath.Join(elsewhere, "evil")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(agent, "codex.sock")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	conn := &codexStatusConn{statuses: []string{`{"type":"active","activeFlags":["waitingOnApproval"]}`}}
	d, dials := &daemonCalls{}, 0
	b := testCodexBridge(conn, d, &dials)
	getenv := func(k string) string { return map[string]string{crew.EnvCodexServer: link}[k] }
	b.server = func() (string, error) { return codexServer(getenv) }
	st := looks(t, b, nil, bridgeRefusals+1) // fails the cell if a look stops the bridge
	if dials != 0 {
		t.Errorf("a server outside Codex's socket folder was dialed %d times", dials)
	}
	if got := d.take(); len(got) != 0 {
		t.Errorf("looks at a refused server told the daemon %q, want nothing", got)
	}
	if st.gone != 0 {
		t.Errorf("a refused server counted %d looks gone, want none: it is no proof the server is gone", st.gone)
	}
}

// TestTheCodexBridgeStopsWhenItsSessionOrServerEnds: the bridge stops when the daemon refuses the
// session's credential, and when the app-server refuses bridgeRefusals dials in a row; fewer, with a
// read between, don't stop it. Its first look clears what an earlier bridge may have raised (the
// mutants: a refused credential ignored; a gone server never counted; one refusal taken for gone;
// the first look's clear skipped).
func TestTheCodexBridgeStopsWhenItsSessionOrServerEnds(t *testing.T) {
	t.Run("the credential refused", func(t *testing.T) {
		conn := &codexStatusConn{statuses: []string{`{"type":"idle"}`}}
		d, dials := &daemonCalls{refuse: func(string) error {
			return &golibrpc.Error{Code: crew.CodeBadCredential, Message: "refused"}
		}}, 0
		b := testCodexBridge(conn, d, &dials)
		tick := make(chan time.Time, 1)
		tick <- time.Now()
		done := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { done <- b.run(ctx, tick, io.Discard) }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "credential is refused") {
				t.Errorf("the bridge stopped with %v, want the refused credential", err)
			}
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatal("the bridge kept running after its credential was refused")
		}
		if conn.closed != 1 {
			t.Errorf("the stopped bridge closed its connection %d times, want once", conn.closed)
		}
		// its first look found no prompt, so it cleared what an earlier bridge may have raised
		if got := d.take(); !slices.Equal(got, []string{"crew.attention clear"}) {
			t.Errorf("the bridge's first look told the daemon %q, want the clear an earlier bridge may need", got)
		}
	})
	t.Run("the server gone", func(t *testing.T) {
		conn := &codexStatusConn{}
		d := &daemonCalls{}
		// two refusals; a connection that reads once, then fails; three refusals in a row
		refused := []bool{true, true, false, true, true, true}
		n := 0
		b := &codexBridge{c: sessionContext{Credential: "cred", Kind: "codex"}, thread: "t1", bound: time.Second,
			server: func() (string, error) { return "/codex/sock", nil },
			dial: func(context.Context, string) (codexConn, error) {
				r := refused[min(n, len(refused)-1)]
				n++
				if r {
					return nil, fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
				}
				conn.mu.Lock()
				conn.statuses, conn.n = []string{`{"type":"idle"}`, "fail"}, 0
				conn.mu.Unlock()
				return conn, nil
			},
			call: d.call}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		tick := make(chan time.Time, 16)
		for range 16 {
			tick <- time.Now()
		}
		done := make(chan error, 1)
		go func() { done <- b.run(ctx, tick, io.Discard) }()
		var err error
		select {
		case err = <-done:
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatalf("the bridge kept running through %d dials", n)
		}
		if !errors.Is(err, errCodexServerGone) {
			t.Fatalf("the bridge stopped with %v, want the server gone", err)
		}
		if n != 6 {
			t.Errorf("it stopped after %d dials, want 6: two refusals, a connection that read, then three refusals in a row", n)
		}
	})
}

// TestTheCodexBridgeFindsItsThreadAsCodexHereDid: the bridge's thread is the folder's, made at or
// after since, looked for again while not there yet, and given up after codexThreadTries looks.
func TestTheCodexBridgeFindsItsThreadAsCodexHereDid(t *testing.T) {
	saved := codexThreadEvery
	codexThreadEvery = time.Millisecond
	t.Cleanup(func() { codexThreadEvery = saved })
	var sinces []int64
	find := func(answers ...error) func(int64) (string, error) {
		n := 0
		return func(since int64) (string, error) {
			sinces = append(sinces, since)
			err := answers[min(n, len(answers)-1)]
			n++
			if err != nil {
				return "", err
			}
			return "t1", nil
		}
	}
	if thread, err := codexBridgeThread(context.Background(), 100, find(errNoCodexThreadHere, nil)); err != nil || thread != "t1" {
		t.Errorf("found on the second look: %q %v", thread, err)
	}
	sinces = nil
	if _, err := codexBridgeThread(context.Background(), 100, find(errNoCodexThreadHere)); !errors.Is(err, errNoCodexThreadHere) || len(sinces) != codexThreadTries {
		t.Errorf("never found: %v after %d looks, want it given up after %d", err, len(sinces), codexThreadTries)
	}
	for _, s := range sinces {
		if s != 100 {
			t.Errorf("looked for a thread made since %d, want 100", s)
		}
	}
}

// TestARegisteredCodexSessionStartsItsBridge: once `crew endpoint --codex-here --wait` has
// registered the session, it starts its Codex bridge with the same --since; a session it couldn't
// register gets none (the mutants: no bridge started; started on a failure; --since dropped).
func TestARegisteredCodexSessionStartsItsBridge(t *testing.T) {
	ctxFile := filepath.Join(t.TempDir(), "ctx")
	if err := os.WriteFile(ctxFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		rest   []string
		answer int
		want   [][]string
	}{
		{"registered", []string{"--codex-here", "--since", "100"}, 0,
			[][]string{{"crew", "endpoint", "--codex-here", "--since", "100"}, {"crew", "bridge", "--codex", "--since", "100"}}},
		{"registered, --since=", []string{"--codex-here", "--since=200"}, 0,
			[][]string{{"crew", "endpoint", "--codex-here", "--since=200"}, {"crew", "bridge", "--codex", "--since", "200"}}},
		{"usage", []string{"--codex-here", "--since", "100"}, 2,
			[][]string{{"crew", "endpoint", "--codex-here", "--since", "100"}}},
	} {
		var runs [][]string
		run := func(args []string, _, _ io.Writer) int {
			runs = append(runs, args)
			if args[1] == "endpoint" {
				return c.answer
			}
			return 0
		}
		code := codexRegisterThenReport(context.Background(), c.rest, time.Minute, time.Millisecond, ctxFile, run, io.Discard, io.Discard)
		if code != c.answer || !slices.EqualFunc(runs, c.want, slices.Equal) {
			t.Errorf("%s: exit %d, runs %q, want %d and %q", c.name, code, runs, c.answer, c.want)
		}
	}
}

// TestTheCodexBridgeStopsWhenItsAgentEnds (no orphan bridge): a thread whose TUI quit stays loaded
// and idle on the shared app-server (codex-cli 0.163.0, live), so the bridge stops when the
// session's context file is gone (its host removes it as the agent ends), at the next look, before
// any read; and when the thread is gone, the app-server answering "thread not loaded" or a
// notLoaded status, for bridgeRefusals looks in a row, a read between starting the count again; the
// connection stands through a thread that isn't loaded (the mutants: the context not looked at;
// "thread not loaded" not taken for gone; notLoaded not counted; the count not reset by a read;
// one look taken for gone).
func TestTheCodexBridgeStopsWhenItsAgentEnds(t *testing.T) {
	t.Run("the context gone", func(t *testing.T) {
		ctxFile := filepath.Join(t.TempDir(), "session")
		if err := os.WriteFile(ctxFile, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		conn := &codexStatusConn{statuses: []string{`{"type":"idle"}`}}
		d, dials := &daemonCalls{}, 0
		b := testCodexBridge(conn, d, &dials)
		b.ctxFile = ctxFile
		st := looks(t, b, nil, 1)
		if err := os.Remove(ctxFile); err != nil {
			t.Fatal(err)
		}
		d.take()
		err := b.look(context.Background(), st, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "context") {
			t.Fatalf("a look after the context went: %v, want the bridge stopped", err)
		}
		if len(conn.calls) != 1 || len(d.take()) != 0 {
			t.Errorf("the look after the context went read %d times in all and told the daemon something; want nothing more", len(conn.calls))
		}
	})
	for _, c := range []struct{ name, gone string }{
		{"the app-server answers thread not loaded", "not loaded"},
		{"the status is notLoaded", `{"type":"notLoaded"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := &codexStatusConn{statuses: []string{c.gone, c.gone, `{"type":"idle"}`, c.gone, c.gone, c.gone}}
			d, dials := &daemonCalls{}, 0
			b := testCodexBridge(conn, d, &dials)
			st := &codexLookState{unknown: true}
			var err error
			n := 0
			for ; n < 6 && err == nil; n++ {
				err = b.look(context.Background(), st, io.Discard)
			}
			if !errors.Is(err, errCodexThreadGone) || n != 6 {
				t.Fatalf("stopped with %v after %d looks, want the thread gone after 6: two gone, a read, three gone", err, n)
			}
			if dials != 1 {
				t.Errorf("%d dials, want the one connection: the server answered every look", dials)
			}
			// only the one read said anything: its clear (the first) and alive; a gone thread, nothing
			if got := d.take(); !slices.Equal(got, []string{"crew.attention clear", "crew.attention alive"}) {
				t.Errorf("the daemon was told %q, want the read's clear and alive alone", got)
			}
		})
	}
}
