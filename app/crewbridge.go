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

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// bridgeEvery is how often a bridge looks at its session, its daemon and its language server.
const bridgeEvery = 30 * time.Second

// bridgeRefusals is how many looks in a row the language server must refuse a connection before
// the bridge takes it as gone: one refusal may be a restart in progress.
const bridgeRefusals = 3

// crewBridgeMain runs `autodoc crew bridge --agy` (ADR 1793271500 §4.7). An Antigravity agent runs
// it once from its shell, the only place its language server's address and token are (its hooks get
// neither): it registers the session's agy endpoint with the token, and stays, detached, to register
// again whenever the daemon restarts (which forgets the token). It is the bridge of one session, the
// one whose context it read as it started; it never reads another, and it exits when that session
// ends or its language server is gone.
func crewBridgeMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autodoc crew bridge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	agy := fs.Bool("agy", false, "an Antigravity session's bridge, from the agent's shell")
	foreground := fs.Bool("foreground", false, "run here, not detached (what the detached bridge runs)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*agy || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: autodoc crew bridge --agy")
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	envFile := os.Getenv("AUTODOC_CREW_CONTEXT")
	c, err := findSessionContext(envFile, cwd)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	b, err := newAgyBridge(c, os.Getenv)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	dir := bridgeDir(envFile, cwd)
	if !*foreground {
		pid, logPath, err := detachBridge(dir, c.Session, args)
		if err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
		fmt.Fprintf(stdout, "the agy bridge runs in the background (pid %d); its log is %s\n", pid, logPath)
		return 0
	}
	unlock, err := lockBridge(dir, c.Session)
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
	err = b.run(ctx, tick.C, stderr)
	fmt.Fprintln(stderr, "autodoc: the agy bridge stops:", err)
	return 0
}

// agyBridge is one session's bridge: its context, as read when it started, and its language server.
type agyBridge struct {
	c       sessionContext
	locator string // {"address", "conversation"}, the address a loopback IP
	token   string
	addr    string

	call     func(ctx context.Context, method string, params ...any) (any, error)
	instance func(ctx context.Context) (string, error) // the daemon's instance
	dial     func(ctx context.Context) error           // a connection to the language server
	hosts    func(ctx context.Context) error           // it hosts the conversation, for the token
}

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
	b := &agyBridge{c: c, locator: string(loc), token: token, addr: ip}
	b.call = func(ctx context.Context, method string, params ...any) (any, error) {
		return crewCall(ctx, c, method, params)
	}
	b.instance = func(ctx context.Context) (string, error) {
		info, err := rpc.Probe(ctx, "unix", c.Socket)
		return info.Instance, err
	}
	client := crew.AgyClient()
	b.hosts = func(ctx context.Context) error { return crew.AgyHosts(ctx, client, ip, token, conv) }
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
// bridgeRefusals looks in a row, or with ctx. A daemon it cannot reach is tried again at the next.
func (b *agyBridge) run(ctx context.Context, tick <-chan time.Time, log io.Writer) error {
	registered, refusals := "", 0
	look := func() error {
		inst, err := b.instance(ctx)
		switch {
		case err != nil:
			fmt.Fprintln(log, "autodoc: the daemon is not answering; trying again:", err)
			registered = ""
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

// bridgeFiles are the session's bridge lock and log in dir, named by the session, so a later session
// in the same folder has its own.
func bridgeFiles(dir, session string) (lock, log string) {
	return filepath.Join(dir, "agy-bridge-"+session+".lock"), filepath.Join(dir, "agy-bridge-"+session+".log")
}
