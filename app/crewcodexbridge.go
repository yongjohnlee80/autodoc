package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The Codex bridge (`autodoc crew bridge --codex`) reports a Codex session's permission prompts
// (docs/decisions/crew.md#the-human-inbox): Codex tells only the client driving its thread, so the
// bridge reads the thread's status on the app-server the session's TUI is attached to (thread/read,
// metadata only: no resume, no subscription, so no approval is ever routed to it). An active thread
// waiting on an approval (a command, a patch, an MCP tool's elicitation) or on the user's input
// raises the session's permission attention, and anything else clears it. Each look that read the
// status says the reporter is alive (crew.attention alive), so a window trusts the session's prompts
// only while the bridge runs. `crew endpoint --codex-here --wait` starts it, detached, once it has
// registered the session.

// codexBridgeEvery is how often the bridge reads its thread's status.
const codexBridgeEvery = time.Second

// codexBridgeLook bounds one look: a dial, its handshake and the read (a read takes about 100 ms
// live, codex-cli 0.163.0), and the daemon's calls.
const codexBridgeLook = 5 * time.Second

// codexThreadTries is how many times the bridge looks for its session's thread as it starts, every
// codexThreadEvery: codex-here has just found it, so the first look normally does.
const codexThreadTries = 5

// codexThreadEvery is how often the bridge looks for its thread again. A cell gives its own.
var codexThreadEvery = codexWaitEvery

// errCodexServerGone is an app-server that isn't there: its socket gone, or refusing connections.
var errCodexServerGone = errors.New("the Codex app-server is gone")

// errCodexThreadGone is a thread the app-server no longer has loaded: deleted, or unloaded. A
// thread whose TUI quit stays loaded on the shared app-server, idle (codex-cli 0.163.0, live
// 2026-11-01): there the session's context file going is what ends the bridge.
var errCodexThreadGone = errors.New("the Codex thread is gone")

// codexThreadNotLoaded is the app-server's refusal of thread/read for a thread it hasn't loaded:
// JSON-RPC's invalid request, "thread not loaded: <id>".
const codexThreadNotLoaded = -32600

// codexConn is one connection to the app-server.
type codexConn interface {
	codexRPC
	close()
}

// codexStatus is a Codex thread's status (ThreadStatus): its type (notLoaded, idle, systemError,
// active) and, when active, what it waits on.
type codexStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

// prompt is what the status shows: an active thread waiting on an approval or on the user's input
// shows a prompt, saying which; anything else shows none.
func (s codexStatus) prompt() (asks string, showing bool) {
	if s.Type != "active" {
		return "", false
	}
	switch {
	case slices.Contains(s.ActiveFlags, "waitingOnApproval"):
		return "Codex waits on an approval", true
	case slices.Contains(s.ActiveFlags, "waitingOnUserInput"):
		return "Codex waits on your input", true
	}
	return "", false
}

// codexBridge is one session's bridge: its context, as read when it started, and its thread.
type codexBridge struct {
	c       sessionContext
	thread  string
	ctxFile string        // the session's context file, which its host removes when the agent ends
	bound   time.Duration // a look's bound (codexBridgeLook)

	// server is the app-server's socket, resolved and bounded to Codex's socket folder (codexServer)
	server func() (string, error)
	dial   func(ctx context.Context, server string) (codexConn, error)
	call   func(ctx context.Context, method string, params ...any) (any, error) // the daemon
}

func newCodexBridge(c sessionContext, thread, ctxFile string, getenv func(string) string) (*codexBridge, error) {
	if c.Kind != "" && c.Kind != "codex" {
		return nil, fmt.Errorf("this session is a %s session, not a Codex one", c.Kind)
	}
	b := &codexBridge{c: c, thread: thread, ctxFile: ctxFile, bound: codexBridgeLook}
	b.server = func() (string, error) { return codexServer(getenv) }
	b.dial = func(ctx context.Context, server string) (codexConn, error) { return dialCodexServer(ctx, server) }
	b.call = func(ctx context.Context, method string, params ...any) (any, error) {
		return crewCall(ctx, c, method, params)
	}
	return b, nil
}

// codexLookState is what the bridge carries from one look to the next.
type codexLookState struct {
	conn    codexConn // the app-server connection, kept while reads succeed
	raised  string    // what the attention raised says ("" for none)
	unknown bool      // an attention an earlier bridge of this session raised may stand
	failing bool      // the last look failed (logged once until one succeeds)
	gone    int       // looks in a row that found the app-server gone
	unseen  int       // looks in a row that found the thread gone
}

// run reports the thread's prompts, a look at each tick, until ctx ends or a look stops it.
func (b *codexBridge) run(ctx context.Context, tick <-chan time.Time, log io.Writer) error {
	// unknown: an attention an earlier bridge of this session raised is cleared at the first look that
	// finds no prompt, since nothing else would
	st := &codexLookState{unknown: true}
	defer func() {
		if st.conn != nil {
			st.conn.close()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
		}
		if err := b.look(ctx, st, log); err != nil {
			return err
		}
	}
}

// look reads the thread's status once, bounded (b.bound): a prompt showing raises the session's
// permission attention, saying what it waits on (again if another took its place), and none showing
// clears what was raised. A look that read the status then says the reporter is alive. A look that
// fails (a server outside Codex's socket folder included: never dialed) raises nothing, clears
// nothing and says nothing alive. It answers an error, which stops the bridge, when the session has
// ended: its context file gone (its host removes it as the agent ends, whatever server it was on),
// or its credential refused (or its folder now holds another's); or when, for bridgeRefusals looks
// in a row, the app-server is gone (an agent's own server ends with its CLI) or the thread is.
func (b *codexBridge) look(ctx context.Context, st *codexLookState, log io.Writer) error {
	if b.ctxFile != "" {
		if _, err := os.Stat(b.ctxFile); errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("the session's context %s is gone: the agent ended", b.ctxFile)
		}
	}
	lctx, cancel := context.WithTimeout(ctx, b.bound)
	defer cancel()
	status, err := b.read(lctx, &st.conn)
	if err == nil && status.Type == "notLoaded" {
		err = fmt.Errorf("%w: its status is notLoaded", errCodexThreadGone)
	}
	if !errors.Is(err, errCodexThreadGone) {
		st.unseen = 0
	}
	if err != nil {
		if errors.Is(err, errCodexServerGone) {
			if st.gone++; st.gone >= bridgeRefusals {
				return fmt.Errorf("%w: %d looks in a row", err, st.gone)
			}
		}
		if errors.Is(err, errCodexThreadGone) {
			if st.unseen++; st.unseen >= bridgeRefusals {
				return fmt.Errorf("%w: %d looks in a row", err, st.unseen)
			}
		}
		if !st.failing && ctx.Err() == nil {
			fmt.Fprintln(log, "autodoc: reading the Codex thread's status; trying again:", err)
		}
		st.failing = true
		return nil
	}
	st.gone, st.failing = 0, false
	asks, showing := status.prompt()
	var callErr error
	switch {
	case showing && asks != st.raised:
		if _, callErr = b.call(lctx, "crew.attention", b.c.Credential, "raise", "permission", asks); callErr == nil {
			st.raised, st.unknown = asks, false
		}
	case !showing && (st.raised != "" || st.unknown):
		if _, callErr = b.call(lctx, "crew.attention", b.c.Credential, "clear"); callErr == nil {
			st.raised, st.unknown = "", false
		}
	}
	if callErr == nil {
		// the look read the thread's status: the prompts are reported (crew.list's prompts_reported)
		_, callErr = b.call(lctx, "crew.attention", b.c.Credential, "alive")
	}
	if ended(callErr) {
		return fmt.Errorf("the session's credential is refused: %w", callErr)
	}
	return nil
}

// read is the thread's status, through *conn, dialed first when there is none. A server whose
// socket is gone or refuses the connection is errCodexServerGone; a failed read closes the
// connection, so the next look dials again.
func (b *codexBridge) read(ctx context.Context, conn *codexConn) (codexStatus, error) {
	if *conn == nil {
		server, err := b.server()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return codexStatus{}, fmt.Errorf("%w: %v", errCodexServerGone, err)
			}
			return codexStatus{}, err
		}
		c, err := b.dial(ctx, server)
		if err != nil {
			if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist) {
				return codexStatus{}, fmt.Errorf("%w: %v", errCodexServerGone, err)
			}
			return codexStatus{}, err
		}
		*conn = c
	}
	var r struct {
		Thread struct {
			Status codexStatus `json:"status"`
		} `json:"thread"`
	}
	if err := (*conn).call(ctx, "thread/read", map[string]any{"threadId": b.thread, "includeTurns": false}, &r); err != nil {
		var re *codexRPCError
		if errors.As(err, &re) && re.Code == codexThreadNotLoaded && strings.HasPrefix(re.Message, "thread not loaded") {
			return codexStatus{}, fmt.Errorf("%w: %v", errCodexThreadGone, err) // the server answered: the connection stands
		}
		(*conn).close()
		*conn = nil
		return codexStatus{}, err
	}
	return r.Thread.Status, nil
}

// codexBridgeThread is the session's thread for the bridge: this folder's, made at or after since,
// as codex-here found it, looked for up to codexThreadTries times.
func codexBridgeThread(ctx context.Context, since int64, find func(int64) (string, error)) (string, error) {
	var err error
	for i := range codexThreadTries {
		var thread string
		if thread, err = find(since); err == nil {
			return thread, nil
		}
		if i+1 == codexThreadTries {
			break
		}
		// NOTE: can't announce: Codex says nothing when its thread is loaded, so it is looked for
		// again after codexThreadEvery, at most codexThreadTries times.
		t := time.NewTimer(codexThreadEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return "", ctx.Err()
		case <-t.C:
		}
	}
	return "", err
}

// crewCodexBridgeMain runs `autodoc crew bridge --codex --since T`: detached (bridgeChild) unless
// foreground, then, holding the session's bridge lock, reports its thread's prompts until run ends.
func crewCodexBridgeMain(ctx context.Context, c sessionContext, envFile, cwd string, since int64, foreground bool, stdout, stderr io.Writer) int {
	dir := bridgeDir(envFile, cwd)
	if !foreground {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
		argv, env := bridgeChild(exe, contextFile(envFile, cwd, c), c.Session,
			[]string{"--codex", "--since", strconv.FormatInt(since, 10)}, os.Environ())
		pid, logPath, err := detachBridge(dir, "codex", c.Session, argv, env)
		if err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
		fmt.Fprintf(stdout, "the Codex bridge runs in the background (pid %d); its log is %s\n", pid, logPath)
		return 0
	}
	unlock, err := lockBridge(dir, "codex", c.Session)
	if errors.Is(err, errBridgeRunning) {
		fmt.Fprintln(stderr, "autodoc: this session's Codex bridge is already running")
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	defer unlock()
	thread, err := codexBridgeThread(ctx, since, codexThreadOfThisFolder)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc: the Codex bridge found no thread:", err)
		return 1
	}
	b, err := newCodexBridge(c, thread, contextFile(envFile, cwd, c), os.Getenv)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	fmt.Fprintln(stderr, "autodoc: the Codex bridge reports thread", thread)
	// NOTE: a look every codexBridgeEvery is how a prompt is seen: Codex tells no one but the client
	// driving the thread.
	tick := time.NewTicker(codexBridgeEvery)
	defer tick.Stop()
	err = b.run(ctx, tick.C, stderr)
	fmt.Fprintln(stderr, "autodoc: the Codex bridge stops:", err)
	return 0
}
