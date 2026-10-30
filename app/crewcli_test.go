package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/todo/sqlitejournal"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// crewDaemon is a daemon serving the crew, mail and inbox modules on a temp store, for the agent's
// commands to reach through a session context.
type crewDaemon struct {
	t    *testing.T
	ctx  context.Context
	sock string
	deps *moduleDeps
	run  string
	ep   int64
}

func newCrewDaemon(t *testing.T) *crewDaemon {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	d := &crewDaemon{t: t, ctx: ctx, sock: short(t) + "/c.sock", run: "run-a"}
	d.deps = &moduleDeps{store: db, storeID: "store-1", workspaces: rpc.Fixed(), journal: sqlitejournal.New(db), log: logger.Nop{}}
	ms := modules(d.deps)
	if err := d.deps.mail.EnsureReserved(ctx); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", d.sock)
	if err != nil {
		t.Fatal(err)
	}
	srvCtx, cancel := context.WithCancel(ctx)
	srv := rpc.New(rpc.Fixed(), "v", rpc.WithListener(ln), rpc.WithModules(ms...), rpc.WithStoreID("store-1"))
	done := make(chan error, 1)
	go func() { done <- srv.Run(srvCtx) }()
	t.Cleanup(func() { cancel(); <-done })
	if d.ep, err = d.deps.crew.Claim(ctx, "autodoc:aaaa", d.run, ""); err != nil {
		t.Fatal(err)
	}
	return d
}

// agent registers codename and writes its session context into a folder of its own, as a host
// does before the agent starts (§4.5); store is the context's store id.
func (d *crewDaemon) agent(codename, instance, store string) string {
	d.t.Helper()
	secret := strings.Repeat("s", 32) + instance
	r, err := d.deps.crew.Register(d.ctx, crew.Registration{Scope: "autodoc:aaaa", Run: d.run, Epoch: d.ep,
		Codename: codename, Kind: "claude", Workspace: "w", Instance: instance, Secret: secret})
	if err != nil {
		d.t.Fatal(err)
	}
	dir := d.t.TempDir()
	b, _ := json.Marshal(sessionContext{Store: store, Socket: d.sock, Scope: "autodoc:aaaa", Profile: r.Profile,
		Session: r.Session, Credential: crew.Credential(r.Session, secret), Kind: "claude"})
	if err := os.WriteFile(filepath.Join(dir, crewContextFile), b, 0o600); err != nil {
		d.t.Fatal(err)
	}
	return dir
}

// as runs `autodoc <args>` as the agent whose context folder is dir (AUTODOC_CREW_CONTEXT), from
// the working directory the test is in, with stdin.
func (d *crewDaemon) as(dir, stdin string, args ...string) (int, map[string]any, string) {
	d.t.Helper()
	d.t.Setenv("AUTODOC_CREW_CONTEXT", dir)
	var out, errOut bytes.Buffer
	code := crewMain(d.ctx, args, strings.NewReader(stdin), &out, &errOut)
	var res map[string]any
	if code == 0 {
		var v any
		if err := json.Unmarshal(out.Bytes(), &v); err != nil {
			d.t.Fatalf("%v: the answer is not JSON: %q", args, out.String())
		}
		res, _ = v.(map[string]any)
		if res == nil {
			res = map[string]any{"list": v}
		}
	}
	return code, res, errOut.String()
}

// TestTheAgentsCommandsSendReceiveAndReply: the commands the guide names, end to end on a served
// store: whoami, a send to a peer, its receive (whose ids carry the lease) and ack, a decision
// request to the human answered through the inbox, and that answer received by its asker.
func TestTheAgentsCommandsSendReceiveAndReply(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir()) // a working directory with no context of its own
	gold, jarvis := d.agent("gold", "agent-1", "store-1"), d.agent("jarvis", "agent-2", "store-1")
	if code, who, errOut := d.as(gold, "", "crew", "whoami"); code != 0 || who["codename"] != "gold" {
		t.Fatalf("crew whoami: exit %d, %v, %s", code, who, errOut)
	}
	if code, _, errOut := d.as(gold, "the plan", "mail", "send", "jarvis", "--subject", "plan", "--body", "-"); code != 0 {
		t.Fatalf("mail send: exit %d, %s", code, errOut)
	}
	// the same command again is the same send: its default key is the content's digest
	if code, _, errOut := d.as(gold, "the plan", "mail", "send", "jarvis", "--subject", "plan", "--body", "-"); code != 0 {
		t.Fatalf("mail send again: exit %d, %s", code, errOut)
	}
	code, got, errOut := d.as(jarvis, "", "mail", "receive")
	ds, _ := got["deliveries"].([]any)
	if code != 0 || len(ds) != 1 || got["more"] != false {
		t.Fatalf("jarvis's mail receive: exit %d, %v, %s; want one delivery, more false", code, got, errOut)
	}
	item := ds[0].(map[string]any)
	if msg := item["message"].(map[string]any); msg["body"] != "the plan" {
		t.Fatalf("the delivery's message: %v", msg)
	}
	id, _ := item["id"].(string)
	if code, _, errOut := d.as(jarvis, "", "mail", "ack", id); code != 0 {
		t.Fatalf("mail ack %s: exit %d, %s", id, code, errOut)
	}
	// a decision request to the human, answered in the inbox; the answer comes back as mail
	if code, _, errOut := d.as(gold, "", "mail", "send", "user", "--kind", "request", "--subject", "Decision: ship?", "--body", "now or later"); code != 0 {
		t.Fatalf("mail send user: exit %d, %s", code, errOut)
	}
	w, err := d.deps.inbox.Watch(d.ctx, 0, 0)
	if err != nil || w.Snapshot == nil || len(w.Snapshot.Items) != 1 || w.Snapshot.Items[0].Codename != "gold" {
		t.Fatalf("the human's snapshot: %+v, %v; want gold's request", w, err)
	}
	if _, err := d.deps.inbox.Reply(d.ctx, w.Snapshot.Items[0].Message.ID, "later", "k1"); err != nil {
		t.Fatal(err)
	}
	code, got, errOut = d.as(gold, "", "mail", "receive")
	ds, _ = got["deliveries"].([]any)
	if code != 0 || len(ds) != 1 {
		t.Fatalf("gold's receive of the answer: exit %d, %v, %s", code, got, errOut)
	}
	if msg := ds[0].(map[string]any)["message"].(map[string]any); msg["kind"] != "response" || msg["body"] != "later" {
		t.Fatalf("the answer gold received: %v", msg)
	}
}

// TestContextsNeverMix (§5 case 5): two contexts present and naming different sessions are refused
// before any call; a context for another store is refused by the daemon (crew.context_mismatch);
// with none, the command says this process is no crew agent.
func TestContextsNeverMix(t *testing.T) {
	d := newCrewDaemon(t)
	gold, jarvis := d.agent("gold", "agent-1", "store-1"), d.agent("jarvis", "agent-2", "store-1")
	t.Chdir(gold) // gold's context in the working directory …
	if code, _, errOut := d.as(jarvis, "", "crew", "whoami"); code != 1 || !strings.Contains(errOut, "disagree") {
		t.Fatalf("whoami with gold's context here and jarvis's in the env: exit %d, %s; want refused", code, errOut)
	}
	if code, who, errOut := d.as(gold, "", "crew", "whoami"); code != 0 || who["codename"] != "gold" {
		t.Fatalf("whoami with gold's context in both: exit %d, %v, %s", code, who, errOut)
	}
	t.Chdir(t.TempDir())
	foreign := d.agent("vision", "agent-3", "store-other")
	code, _, errOut := d.as(foreign, "", "crew", "whoami")
	if code != 1 || !strings.Contains(errOut, `"code":-32095`) {
		t.Fatalf("whoami with another store's context: exit %d, %s; want crew.context_mismatch", code, errOut)
	}
	if code, _, errOut := d.as("", "", "crew", "whoami"); code != 1 || !strings.Contains(errOut, "no session context") {
		t.Fatalf("whoami with no context: exit %d, %s", code, errOut)
	}
}

// TestTheCommandsParseAsWritten: flags may follow the recipients, "-" is the body from standard
// input, a delivery id is the delivery and its lease, and an unknown command prints the usage.
func TestTheCommandsParseAsWritten(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	bodyFlags(fs)
	fs.Bool("x", false, "")
	got := interleaved([]string{"gold", "--body", "-", "jarvis@p", "-x", "--subject=s", "--", "--odd"}, fs)
	want := []string{"--body", "-", "-x", "--subject=s", "--", "gold", "jarvis@p", "--odd"}
	if !slices.Equal(got, want) {
		t.Fatalf("interleaved: %q, want %q", got, want)
	}
	if d, l, err := parseHandle("d1:l1"); err != nil || d != "d1" || l != "l1" {
		t.Fatalf("parseHandle: %q %q %v", d, l, err)
	}
	for _, bad := range []string{"d1", ":l1", "d1:"} {
		if _, _, err := parseHandle(bad); err == nil {
			t.Errorf("parseHandle(%q) took it", bad)
		}
	}
	if sendKey([]string{"a"}, "", "s", "b", 0) == sendKey([]string{"a"}, "", "sb", "", 0) {
		t.Error("two sends whose fields shift across a boundary have one key")
	}
	if sendKey([]string{"a"}, "", "s", "b", time.Hour) == sendKey([]string{"a"}, "", "s", "b", 0) {
		t.Error("a ttl changes nothing in the key")
	}
	var errOut bytes.Buffer
	if code := crewMain(context.Background(), []string{"mail", "nope"}, nil, &bytes.Buffer{}, &errOut); code != 2 ||
		!strings.Contains(errOut.String(), "autodoc mail receive") {
		t.Fatalf("an unknown command: exit %d, %s", code, errOut.String())
	}
}

// TestTheGuideNamesOnlyRealCommands: every `autodoc crew|mail …` the agent guide tells a CLI to run
// is one of the commands, so an agent following it never meets "no command". The two waker steps
// phase 3 brings with its wakers are named here as pending, positively: a third would fail.
func TestTheGuideNamesOnlyRealCommands(t *testing.T) {
	pending := map[string]bool{
		"crew endpoint --codex-thread": true, // Codex's waker (phase 3) gives the thread's server
		"crew bridge":                  true, // agy's bridge (phase 3)
	}
	cmd := regexp.MustCompile("`autodoc ((?:crew|mail) [a-z]+)( --[a-z-]+)?")
	m := crew.New(nil)
	seen := map[string]bool{}
	for _, kind := range []string{"claude", "codex", "agy", "opencode", "other"} {
		for _, hit := range cmd.FindAllStringSubmatch(m.GuideFor(kind).Markdown, -1) {
			name, withFlag := hit[1], hit[1]+hit[2]
			seen[name] = true
			if pending[withFlag] || pending[name] {
				continue
			}
			if _, ok := crewCommands[name]; !ok {
				t.Errorf("%s's guide names `autodoc %s`, which is no command", kind, withFlag)
			}
		}
	}
	for _, want := range []string{"crew whoami", "mail receive", "mail reply", "mail ack", "mail renew", "mail send"} {
		if !seen[want] {
			t.Errorf("the guides never name `autodoc %s`: this check reads them wrong", want)
		}
	}
}
