package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// bridgeEvery is how often a bridge looks at its session, its daemon and its language server.
const bridgeEvery = 30 * time.Second

// bridgeLook bounds one look, all it calls included: a daemon or language server that takes a
// connection and never answers must not hold the bridge, so the next look (another daemon, the
// session's end) still comes. Within bridgeEvery, so one look is over before the next is due.
const bridgeLook = 20 * time.Second

// bridgeRefusals is how many looks in a row the language server must refuse a connection before
// the bridge takes it as gone: one refusal may be a restart in progress.
const bridgeRefusals = 3

// crewBridgeMain runs `autodoc crew bridge --agy`
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler). An Antigravity agent runs it once from
// its shell, the only place its language server's address and token are (its hooks get neither): it
// registers the session's agy endpoint with the token, and stays, detached, to register again
// whenever the daemon restarts (which forgets the token). It is the bridge of one session, the one
// whose context it read as it started; it never reads another, and it exits when that session ends
// or its language server is gone.
func crewBridgeMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autodoc crew bridge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	agy := fs.Bool("agy", false, "an Antigravity session's bridge, from the agent's shell")
	codex := fs.Bool("codex", false, "a Codex session's prompt reporter (crew endpoint --codex-here --wait starts it)")
	since := fs.Int64("since", 0, "with --codex: the session's thread was made at or after this Unix time (seconds)")
	foreground := fs.Bool("foreground", false, "run here, not detached (what the detached bridge runs)")
	session := fs.String("session", "", "the session the bridge was started for (the detached bridge's): another is refused")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *agy == *codex || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: autodoc crew bridge --agy | --codex [--since T]")
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	envFile := os.Getenv("AUTODOC_CREW_CONTEXT")
	c, err := pinnedContext(envFile, cwd, *session)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	if *codex {
		return crewCodexBridgeMain(ctx, c, envFile, cwd, *since, *foreground, stdout, stderr)
	}
	b, err := newAgyBridge(c, os.Getenv)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	dir := bridgeDir(envFile, cwd)
	if !*foreground {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
		argv, env := bridgeChild(exe, contextFile(envFile, cwd, c), c.Session, []string{"--agy"}, os.Environ())
		pid, logPath, err := detachBridge(dir, "agy", c.Session, argv, env)
		if err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
		fmt.Fprintf(stdout, "the agy bridge runs in the background (pid %d); its log is %s\n", pid, logPath)
		return 0
	}
	unlock, err := lockBridge(dir, "agy", c.Session)
	if errors.Is(err, errBridgeRunning) {
		fmt.Fprintln(stderr, "autodoc: this session's agy bridge is already running")
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	defer unlock()
	// NOTE: a look every bridgeEvery is how a daemon restart (which forgets the token) and the
	// language server's end are seen: neither tells the bridge.
	tick := time.NewTicker(bridgeEvery)
	defer tick.Stop()
	// NOTE: a look every bridgePromptEvery is how a permission prompt is seen: the language server
	// tells no one but its own client.
	prompts := time.NewTicker(bridgePromptEvery)
	defer prompts.Stop()
	watching, stopWatching := context.WithCancel(ctx)
	watched := make(chan struct{})
	go func() { defer close(watched); b.watchPrompts(watching, prompts.C, stderr) }()
	err = b.run(ctx, tick.C, stderr)
	stopWatching()
	<-watched
	fmt.Fprintln(stderr, "autodoc: the agy bridge stops:", err)
	return 0
}

// pinnedContext is the session context as findSessionContext finds it, refused when session is
// set and the context is another's: the detached bridge is told the session it was started for,
// and never takes up a replacement found in its place.
func pinnedContext(envFile, cwd, session string) (sessionContext, error) {
	c, err := findSessionContext(envFile, cwd)
	if err != nil {
		return sessionContext{}, err
	}
	if session != "" && c.Session != session {
		return sessionContext{}, fmt.Errorf("the session context is %s's, not %s's, which this bridge was started for: stopping", c.Session, session)
	}
	return c, nil
}

// contextFile is the file c was read from: the one $AUTODOC_CREW_CONTEXT names (envFile), else the
// working directory's, named by its session.
func contextFile(envFile, cwd string, c sessionContext) string {
	if envFile != "" {
		return envFile
	}
	return crew.ContextPath(cwd, c.Session)
}

// bridgeChild is the detached bridge's command line and environment: this executable run again in
// the foreground with the kind's flags (kindArgs: --agy, or --codex and its --since), pinned to the
// context the parent read (path, as $AUTODOC_CREW_CONTEXT, so it reads that file or none, never one
// found anew in the working directory) and to its session. The token stays in the environment,
// inherited, never in an argument.
func bridgeChild(exe, path, session string, kindArgs, environ []string) (argv, env []string) {
	argv = append(append([]string{exe, "crew", "bridge"}, kindArgs...), "--foreground", "--session", session)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "AUTODOC_CREW_CONTEXT=") {
			env = append(env, kv)
		}
	}
	return argv, append(env, "AUTODOC_CREW_CONTEXT="+path)
}

// agyBridge is one session's bridge: its context, as read when it started, and its language server.
type agyBridge struct {
	c       sessionContext
	locator string // {"address", "conversation"}, the address a loopback IP
	token   string
	addr    string
	bound   time.Duration // a look's bound (bridgeLook)

	call     func(ctx context.Context, method string, params ...any) (any, error)
	instance func(ctx context.Context) (string, error) // the daemon's instance
	dial     func(ctx context.Context) error           // a connection to the language server
	hosts    func(ctx context.Context) error           // it hosts the conversation, for the token
	// prompt reads the conversation's permission prompt from step offset on (crew.AgyPromptOf)
	prompt func(ctx context.Context, offset int) (crew.AgyPrompt, int, error)
	// running reports whether the conversation has a turn running (crew.AgyTurnRunning); nil
	// registers at once. The first registration waits for the launch turn (awaitLaunchTurn), looking
	// every turnEvery (through sleep) for at most turnBound (by now).
	running              func(ctx context.Context) (bool, error)
	turnEvery, turnBound time.Duration
	sleep                func(ctx context.Context, d time.Duration) bool
	now                  func() time.Time
}

// The first registration's wait for the launch turn (awaitLaunchTurn): how often it looks, and for
// how long at most.
const (
	bridgeTurnEvery = 2 * time.Second
	bridgeTurnBound = 2 * time.Minute
)

// newAgyBridge is c's bridge from the language server getenv names, its address made a loopback IP.
func newAgyBridge(c sessionContext, getenv func(string) string) (*agyBridge, error) {
	if c.Kind != "" && c.Kind != "agy" {
		return nil, fmt.Errorf("this session is a %s session, not an agy one", c.Kind)
	}
	addr, token, conv := getenv("ANTIGRAVITY_LS_ADDRESS"), getenv("ANTIGRAVITY_CSRF_TOKEN"), getenv("ANTIGRAVITY_CONVERSATION_ID")
	if addr == "" || token == "" || conv == "" {
		return nil, errors.New("no Antigravity language server here: run this from the agy agent's own shell, where $ANTIGRAVITY_LS_ADDRESS, $ANTIGRAVITY_CSRF_TOKEN and $ANTIGRAVITY_CONVERSATION_ID are set")
	}
	ip, err := loopbackAddress(addr)
	if err != nil {
		return nil, err
	}
	loc, err := json.Marshal(map[string]string{"address": ip, "conversation": conv})
	if err != nil {
		return nil, err
	}
	b := &agyBridge{c: c, locator: string(loc), token: token, addr: ip, bound: bridgeLook}
	b.call = func(ctx context.Context, method string, params ...any) (any, error) {
		return crewCall(ctx, c, method, params)
	}
	b.instance = func(ctx context.Context) (string, error) {
		info, err := rpc.Probe(ctx, "unix", c.Socket)
		return info.Instance, err
	}
	client := crew.AgyClient()
	b.hosts = func(ctx context.Context) error { return crew.AgyHosts(ctx, client, ip, token, conv) }
	b.prompt = func(ctx context.Context, offset int) (crew.AgyPrompt, int, error) {
		return crew.AgyPromptOf(ctx, client, ip, token, conv, offset)
	}
	b.running = func(ctx context.Context) (bool, error) { return crew.AgyTurnRunning(ctx, client, ip, token, conv) }
	b.turnEvery, b.turnBound, b.sleep, b.now = bridgeTurnEvery, bridgeTurnBound, clock.Sleep, time.Now
	b.dial = func(ctx context.Context) error {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", ip)
		if err == nil {
			conn.Close()
		}
		return err
	}
	return b, nil
}

// loopbackAddress is addr ("host:port", as agy gives it) with its host a loopback IP: localhost is
// 127.0.0.1, never resolved, and any other host must already be a loopback IP.
func loopbackAddress(addr string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimPrefix(addr, "http://"))
	if err != nil {
		return "", fmt.Errorf("the language server's address %q is not host:port", addr)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Unmap().IsLoopback() {
		return "", fmt.Errorf("the language server's address %q is not on loopback", addr)
	}
	return net.JoinHostPort(ip.Unmap().String(), port), nil
}

// run registers the endpoint, and at each tick: registers it again if the daemon is another
// instance than the one it registered with (a restarted daemon has forgotten the token), else checks
// the session is still this one (crew.whoami, which changes nothing: a registration raises the
// generation and resets the session's wake state, so a tick never makes one), and checks the
// language server (a connection, then GetConversationMetadata). It ends when the session's credential is
// refused (the session ended, or its folder now holds another's), when the language server refuses
// bridgeRefusals looks in a row, or with ctx. Each look is bounded (b.bound): a daemon it cannot
// reach, or one that does not answer in time, is tried again at the next.
func (b *agyBridge) run(ctx context.Context, tick <-chan time.Time, log io.Writer) error {
	registered, refusals := "", 0
	look := func() error {
		ctx, cancel := context.WithTimeout(ctx, b.bound)
		defer cancel()
		inst, err := b.instance(ctx)
		switch {
		case err != nil:
			// the registration stands: a restarted daemon is another instance, seen at the next look
			fmt.Fprintln(log, "autodoc: the daemon is not answering; trying again:", err)
		case inst != registered:
			if _, err := b.call(ctx, "crew.endpoint", b.c.Credential, "agy", b.locator, b.token); err != nil {
				if ended(err) {
					return fmt.Errorf("the session's credential is refused: %w", err)
				}
				fmt.Fprintln(log, "autodoc: registering the agy endpoint; trying again:", err)
			} else {
				registered = inst
				fmt.Fprintln(log, "autodoc: the agy endpoint is registered with daemon instance", inst)
			}
		default:
			if _, err := b.call(ctx, "crew.whoami", b.c.Credential, b.c.Store); ended(err) {
				return fmt.Errorf("the session's credential is refused: %w", err)
			}
		}
		if err := b.dial(ctx); errors.Is(err, syscall.ECONNREFUSED) {
			refusals++
			if refusals >= bridgeRefusals {
				return fmt.Errorf("the language server at %s refused %d looks in a row: it is gone", b.addr, refusals)
			}
			return nil
		}
		refusals = 0
		// a failure here is no proof the server is gone (it may be busy, or restarting): logged only
		if err := b.hosts(ctx); err != nil {
			fmt.Fprintln(log, "autodoc: the language server did not confirm the conversation:", err)
		}
		return nil
	}
	b.awaitLaunchTurn(ctx, log)
	if err := look(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
			if err := look(); err != nil {
				return err
			}
		}
	}
}

// awaitLaunchTurn holds the bridge's first registration until the turn that launched it has ended
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): a wake registered sooner lands inside that
// turn, where agy has not yet given the agent its workspace's MCP tools, so its mail_receive is
// missing; once the turn ends, a wake starts the next, which has them. It waits at most turnBound,
// then registers anyway (the scheduler rings until the mail is received, so a late registration
// still delivers); a running state it can't read registers at once. Only the first registration
// waits: a daemon's restart has no launch turn to wait for.
func (b *agyBridge) awaitLaunchTurn(ctx context.Context, log io.Writer) {
	if b.running == nil {
		return
	}
	start := b.now()
	for {
		lctx, cancel := context.WithTimeout(ctx, b.bound)
		running, err := b.running(lctx)
		cancel()
		switch {
		case err != nil:
			fmt.Fprintln(log, "autodoc: can't tell whether the launch turn has ended; registering now:", err)
			return
		case !running:
			return
		case b.now().Sub(start) >= b.turnBound:
			fmt.Fprintf(log, "autodoc: the launch turn still runs after %s; registering anyway\n", b.turnBound)
			return
		}
		// NOTE: agy tells the bridge of no turn's end, so it looks again after turnEvery
		if !b.sleep(ctx, b.turnEvery) {
			return
		}
	}
}

// bridgePromptEvery is how often the bridge looks for the agent's permission prompt.
const bridgePromptEvery = 2 * time.Second

// watchPrompts reports the agent's permission prompts, at each tick until ctx ends: a prompt showing
// raises the session's permission attention, saying what it asks (again if another prompt took its
// place), and none showing clears what it raised (or, at its first look, what an earlier bridge
// may have). Each look that read the language server says the reporter is alive (crew.attention
// alive), so a window trusts the session's prompts only while this runs. Each look is bounded
// (b.bound); a look that fails changes nothing, says nothing, and is logged once until one succeeds.
func (b *agyBridge) watchPrompts(ctx context.Context, tick <-chan time.Time, log io.Writer) {
	// unknown: an attention an earlier bridge of this session raised is cleared at the first look that
	// finds no prompt, since nothing else would
	offset, raised, unknown, failing := 0, "", true, false
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
		lctx, cancel := context.WithTimeout(ctx, b.bound)
		p, next, err := b.prompt(lctx, offset)
		if err != nil {
			cancel()
			if !failing && ctx.Err() == nil {
				fmt.Fprintln(log, "autodoc: looking for the agent's permission prompt; trying again:", err)
			}
			failing = true
			continue
		}
		failing, offset = false, next
		switch {
		case p.Showing && p.Asks != raised:
			if _, err := b.call(lctx, "crew.attention", b.c.Credential, "raise", crew.AttentionPermission, p.Asks); err == nil {
				raised, unknown = p.Asks, false
			}
		case !p.Showing && (raised != "" || unknown):
			if _, err := b.call(lctx, "crew.attention", b.c.Credential, "clear"); err == nil {
				raised, unknown = "", false
			}
		}
		// the look read the language server: the prompts are reported (crew.list's prompts_reported)
		_, _ = b.call(lctx, "crew.attention", b.c.Credential, "alive")
		cancel()
	}
}

// ended reports whether err is the daemon refusing the session's credential (the session ended, or
// was never this one) or its context (another store): either way this bridge is no longer its
// session's.
func ended(err error) bool {
	var re *golibrpc.Error
	return errors.As(err, &re) && (re.Code == crew.CodeBadCredential || re.Code == crew.CodeContextMismatch)
}

// errBridgeRunning is lockBridge's answer when the session's bridge already holds its lock.
var errBridgeRunning = errors.New("the agy bridge is already running")

// bridgeDir is the agent folder a bridge keeps its lock and log in: the one holding the context
// file $AUTODOC_CREW_CONTEXT names (envFile, <agent folder>/.autodoc-crew/<session>), else the
// working directory. Never the contexts' own folder, where every plain file is taken for a context.
func bridgeDir(envFile, cwd string) string {
	if envFile == "" {
		return cwd
	}
	dir := filepath.Dir(envFile)
	if filepath.Base(dir) == crewContextDir {
		dir = filepath.Dir(dir)
	}
	return dir
}

// bridgeFiles are the session's bridge lock and log in dir, named by its kind (agy, codex) and the
// session, so a later session in the same folder has its own.
func bridgeFiles(dir, kind, session string) (lock, log string) {
	return filepath.Join(dir, kind+"-bridge-"+session+".lock"), filepath.Join(dir, kind+"-bridge-"+session+".log")
}
