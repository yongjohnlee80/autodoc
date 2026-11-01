//go:build linux

package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestTheBridgeCommandTakesOneKind: `autodoc crew bridge` is one kind's bridge, --agy or --codex:
// both, neither, or an argument beside them is a usage error (exit 2), before any context is read.
func TestTheBridgeCommandTakesOneKind(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"--agy", "--codex"}, {}, {"--agy", "extra"}, {"--codex", "--since", "1", "extra"}} {
		var out, errOut bytes.Buffer
		if code := crewBridgeMain(context.Background(), args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: autodoc crew bridge --agy | --codex [--since T]") {
			t.Errorf("%q: exit %d, stderr %q; want 2 and the usage line", args, code, errOut.String())
		}
	}
}

// TestTheBridgeCommandNeedsASession: a bridge started where no session context is found (no
// $AUTODOC_CREW_CONTEXT, none in the folder) says so and fails, for either kind.
func TestTheBridgeCommandNeedsASession(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AUTODOC_CREW_CONTEXT", "")
	for _, args := range [][]string{{"--agy", "--foreground"}, {"--codex", "--foreground"}} {
		var out, errOut bytes.Buffer
		if code := crewBridgeMain(context.Background(), args, &out, &errOut); code != 1 || !strings.HasPrefix(errOut.String(), "autodoc: ") {
			t.Errorf("%q with no session: exit %d, stderr %q; want 1 and why", args, code, errOut.String())
		}
	}
}

// TestTheCodexBridgeCommandIsTheCodexBridge: --codex runs the session's Codex bridge, not agy's:
// in the foreground it takes the session's Codex bridge lock (codex-bridge-<session>.lock beside the
// contexts, never agy's) and, finding no Codex thread for the folder, says so and fails.
func TestTheCodexBridgeCommandIsTheCodexBridge(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agentOfKind("bartz", "agent-1", "store-1", crew.KindCodex)
	c, err := readSessionContext(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(agentFolder(path))
	t.Setenv("AUTODOC_CREW_CONTEXT", path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no thread is waited for
	var out, errOut bytes.Buffer
	if code := crewBridgeMain(ctx, []string{"--codex", "--foreground", "--since", "1"}, &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "the Codex bridge found no thread") {
		t.Errorf("--codex with no Codex thread: exit %d, stderr %q; want 1, saying it found no thread", code, errOut.String())
	}
	dir := bridgeDir(path, agentFolder(path))
	if _, err := os.Stat(filepath.Join(dir, "codex-bridge-"+c.Session+".lock")); err != nil {
		t.Errorf("the Codex bridge's lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agy-bridge-"+c.Session+".lock")); err == nil {
		t.Error("--codex took agy's bridge lock")
	}
}

// TestTheAgyBridgeCommandKnowsItsAgy: in the foreground the agy bridge runs until its context ends,
// then says why it stops. Told its agy process (AUTODOC_AGY_PROC, as the detached bridge is), it
// ends with that process; told nothing and run under no agy, it says it can't tell, and ends only
// with its session or its language server.
func TestTheAgyBridgeCommandKnowsItsAgy(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agentOfKind("tifa", "agent-1", "store-1", crew.KindAgy)
	c, err := readSessionContext(path)
	if err != nil {
		t.Fatal(err)
	}
	ls := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	t.Cleanup(ls.Close)
	t.Chdir(agentFolder(path))
	t.Setenv("AUTODOC_CREW_CONTEXT", path)
	t.Setenv("ANTIGRAVITY_LS_ADDRESS", strings.TrimPrefix(ls.URL, "http://"))
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "token")
	t.Setenv("ANTIGRAVITY_CONVERSATION_ID", "conv")
	ref, err := crew.ProcRef(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		proc    string
		unknown bool
	}{{"", true}, {ref, false}} {
		t.Setenv(agyProcEnv, tc.proc)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var out, errOut bytes.Buffer
		code := crewBridgeMain(ctx, []string{"--agy", "--foreground"}, &out, &errOut)
		said := errOut.String()
		if code != 0 || !strings.Contains(said, "autodoc: the agy bridge stops:") {
			t.Errorf("%s=%q: exit %d, stderr %q; want 0, saying why it stops", agyProcEnv, tc.proc, code, said)
		}
		if got := strings.Contains(said, "the agy process this bridge runs under is unknown"); got != tc.unknown {
			t.Errorf("%s=%q: says its agy is unknown %v, want %v (stderr %q)", agyProcEnv, tc.proc, got, tc.unknown, said)
		}
	}
	if _, err := os.Stat(filepath.Join(bridgeDir(path, agentFolder(path)), "agy-bridge-"+c.Session+".lock")); err != nil {
		t.Errorf("the agy bridge's lock: %v", err)
	}
}
