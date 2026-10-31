package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/mail"
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

// agent registers codename and writes its session context into an agent folder of its own, as a
// host does before the agent starts (docs/decisions/crew.md#the-session-context), and answers the
// context file's path, which the agent is told; store is the context's store id.
func (d *crewDaemon) agent(codename, instance, store string) string {
	d.t.Helper()
	secret := strings.Repeat("s", 32) + instance
	r, err := d.deps.crew.Register(d.ctx, crew.Registration{Scope: "autodoc:aaaa", Run: d.run, Epoch: d.ep,
		Codename: codename, Kind: "claude", Workspace: "w", Instance: instance, Secret: secret})
	if err != nil {
		d.t.Fatal(err)
	}
	path, err := crew.WriteContext(d.t.TempDir(), sessionContext{Store: store, Socket: d.sock, Scope: "autodoc:aaaa", Profile: r.Profile,
		Session: r.Session, Credential: crew.Credential(r.Session, secret), Kind: "claude"})
	if err != nil {
		d.t.Fatal(err)
	}
	return path
}

// agentFolder is the agent folder of the context file path.
func agentFolder(path string) string { return filepath.Dir(filepath.Dir(path)) }

// as runs `autodoc <args>` as the agent whose context file is path (AUTODOC_CREW_CONTEXT), from
// the working directory the test is in, with stdin.
func (d *crewDaemon) as(path, stdin string, args ...string) (int, map[string]any, string) {
	d.t.Helper()
	d.t.Setenv("AUTODOC_CREW_CONTEXT", path)
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
	w, err := d.deps.inbox.Watch(d.ctx, 0, 0, "")
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

// TestContextsNeverMix (docs/decisions/crew.md#the-session-context): an env context whose session
// the working directory's contexts don't hold is refused before any call; without the env, the
// working directory's one context is the agent's, and several are refused, never one guessed; a
// context for another store is refused by the daemon (crew.context_mismatch); with none, the
// command says this process is no crew agent.
func TestContextsNeverMix(t *testing.T) {
	d := newCrewDaemon(t)
	gold, jarvis := d.agent("gold", "agent-1", "store-1"), d.agent("jarvis", "agent-2", "store-1")
	t.Chdir(agentFolder(gold)) // gold's context in the working directory …
	if code, _, errOut := d.as(jarvis, "", "crew", "whoami"); code != 1 || !strings.Contains(errOut, "disagree") {
		t.Fatalf("whoami with gold's context here and jarvis's in the env: exit %d, %s; want refused", code, errOut)
	}
	if code, who, errOut := d.as(gold, "", "crew", "whoami"); code != 0 || who["codename"] != "gold" {
		t.Fatalf("whoami with gold's context in both: exit %d, %v, %s", code, who, errOut)
	}
	if code, who, errOut := d.as("", "", "crew", "whoami"); code != 0 || who["codename"] != "gold" {
		t.Fatalf("whoami with gold's context alone here, no env: exit %d, %v, %s", code, who, errOut)
	}
	// a second session's context in the same folder (a replacement, or one a crash left): without the
	// env, neither is taken
	b, err := os.ReadFile(jarvis)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentFolder(gold), crewContextDir, filepath.Base(jarvis)), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := d.as("", "", "crew", "whoami"); code != 1 || !strings.Contains(errOut, "several session contexts") {
		t.Fatalf("whoami with two contexts here, no env: exit %d, %s; want refused", code, errOut)
	}
	if code, who, errOut := d.as(gold, "", "crew", "whoami"); code != 0 || who["codename"] != "gold" {
		t.Fatalf("whoami with two contexts here and gold's in the env: exit %d, %v, %s", code, who, errOut)
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
	for ttl, want := range map[time.Duration]int64{0: 0, time.Nanosecond: 1, 500 * time.Millisecond: 1,
		time.Second: 1, 1500 * time.Millisecond: 2, time.Hour: 3600, math.MaxInt64: math.MaxInt64/int64(time.Second) + 1} {
		if got := ttlSeconds(ttl); got != want {
			t.Errorf("ttlSeconds(%v) = %d, want %d: a positive ttl never rounds to 0, which means never", ttl, got, want)
		}
	}
	sendFS := flag.NewFlagSet("t", flag.ContinueOnError)
	crewCommands["mail send"].flags(sendFS)
	if err := sendFS.Parse([]string{"--ttl", "500ms", "--body", "b"}); err != nil {
		t.Fatal(err)
	}
	if _, params, _, err := crewCommands["mail send"].call(sessionContext{Credential: "c"}, sendFS, []string{"gold"}, nil); err != nil || params[6] != int64(1) {
		t.Fatalf("mail send --ttl 500ms: params %v, %v; want ttl 1 second", params, err)
	}
	for _, neg := range []string{"-1s", "-500ms"} {
		negFS := flag.NewFlagSet("t", flag.ContinueOnError)
		crewCommands["mail send"].flags(negFS)
		if err := negFS.Parse([]string{"--ttl", neg, "--body", "b"}); err != nil {
			t.Fatal(err)
		}
		if _, params, _, err := crewCommands["mail send"].call(sessionContext{Credential: "c"}, negFS, []string{"gold"}, nil); err == nil {
			t.Errorf("mail send --ttl %s: params %v, want it refused, not sent as never", neg, params)
		}
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

// TestTheGuideNamesOnlyRealCommands: every `autodoc crew|mail …` the agent guide tells a CLI to run,
// and every command's help example, is one of the commands, with only flags it defines, so an agent
// following either never meets "no command" or an unknown flag.
func TestTheGuideNamesOnlyRealCommands(t *testing.T) {
	cmd := regexp.MustCompile("`autodoc ((?:crew|mail) [a-z][a-z-]*)([^`]*)`")
	flagRe := regexp.MustCompile(`(?:^|\s)--([a-z][a-z-]*)`)
	check := func(where, name, rest string) {
		t.Helper()
		c, ok := crewCommands[name]
		if !ok {
			t.Errorf("%s names `autodoc %s`, which is no command", where, name)
			return
		}
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		if c.flags != nil {
			c.flags(fs)
		}
		for _, f := range flagRe.FindAllStringSubmatch(rest, -1) {
			if fs.Lookup(f[1]) == nil {
				t.Errorf("%s names `autodoc %s … --%s`, a flag %s doesn't define", where, name, f[1], name)
			}
		}
	}
	m := crew.New(nil)
	seen := map[string]bool{}
	for _, kind := range []string{"claude", "codex", "agy", "opencode", "other"} {
		for _, hit := range cmd.FindAllStringSubmatch(m.GuideFor(kind).Markdown, -1) {
			seen[hit[1]] = true
			check(kind+"'s guide", hit[1], hit[2])
		}
	}
	for _, want := range []string{"crew whoami", "crew list", "mail receive", "mail reply", "mail ack", "mail renew", "mail send"} {
		if !seen[want] {
			t.Errorf("the guides never name `autodoc %s`: this check reads them wrong", want)
		}
	}
	for name, c := range crewCommands {
		hit := cmd.FindStringSubmatch("`" + c.example + "`")
		if hit == nil || hit[1] != name {
			t.Errorf("%s's example %q is not a line of that command", name, c.example)
			continue
		}
		check(name+"'s example", name, hit[2])
	}
}

// TestTheCrewAndMailHelpShowEveryCommandWithAnExample: `autodoc mail` and `autodoc crew`, bare or
// with --help, -h or help, answer (stdout, exit 0) every command of that domain with its usage and
// example, and no other domain's; a command's own --help answers its line and example.
func TestTheCrewAndMailHelpShowEveryCommandWithAnExample(t *testing.T) {
	for _, domain := range []string{"crew", "mail"} {
		other := map[string]string{"crew": "mail", "mail": "crew"}[domain]
		for _, args := range [][]string{{domain}, {domain, "--help"}, {domain, "-h"}, {domain, "help"}} {
			var out, errOut bytes.Buffer
			if code := crewMain(context.Background(), args, nil, &out, &errOut); code != 0 || errOut.Len() != 0 {
				t.Fatalf("%v: exit %d, stderr %q", args, code, errOut.String())
			}
			for name, c := range crewCommands {
				d, _, _ := strings.Cut(name, " ")
				has := strings.Contains(out.String(), "autodoc "+name+" ") && strings.Contains(out.String(), "e.g. "+c.example+"\n")
				if d == domain && !has {
					t.Errorf("%v doesn't show `autodoc %s` and its example", args, name)
				}
				if d == other && strings.Contains(out.String(), "autodoc "+name+" ") {
					t.Errorf("%v shows %s's `autodoc %s`", args, other, name)
				}
			}
		}
	}
	var out, errOut bytes.Buffer
	if code := crewMain(context.Background(), []string{"mail", "send", "--help"}, nil, &out, &errOut); code != 0 ||
		!strings.Contains(out.String(), "e.g. "+crewCommands["mail send"].example) || !strings.Contains(errOut.String(), "-kind") {
		t.Fatalf("mail send --help: exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

// TestASnapshotPastTheMessageLimitPagesThroughRPC: forty sessions wait on the human, each with the
// longest attention text crew accepts (a longer one is refused), and the human has 70 open requests
// of mail's largest body: 7 MB together, past the transport's 4 MiB message. A window that connects
// through the daemon's socket, as every window does, still reads every attention and request:
// inbox.watch answers a first page and then each next page within the limit.
func TestASnapshotPastTheMessageLimitPagesThroughRPC(t *testing.T) {
	d := newCrewDaemon(t)
	// the sender has the longest codename crew accepts, which every one of its items carries
	c, err := readSessionContext(d.agent(strings.Repeat("g", crew.MaxName), "agent-1", "store-1"))
	if err != nil {
		t.Fatal(err)
	}
	// forty sessions waiting on the user, each with the longest attention text crew accepts, and one
	// longer that crew refuses
	text := strings.Repeat("a", crew.MaxAttentionText)
	for i := range 40 {
		a, err := readSessionContext(d.agent(fmt.Sprintf("w%02d", i), fmt.Sprintf("agent-w%02d", i), "store-1"))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.deps.crew.RaiseAttention(d.ctx, a.Credential, crew.AttentionPermission, text); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := d.deps.crew.RaiseAttention(d.ctx, a.Credential, crew.AttentionPermission, text+"!"); !errors.Is(err, crew.ErrInvalid) {
				t.Fatalf("an attention past the longest text: %v, want ErrInvalid (refused, never truncated)", err)
			}
		}
	}
	body := strings.Repeat("x", mail.MaxBody)
	for i := range 70 {
		if _, err := d.deps.mail.Send(d.ctx, c.Credential, mail.Send{To: []string{mail.UserCodename}, Kind: mail.KindRequest,
			Subject: fmt.Sprintf("big%02d", i), Body: body, Key: fmt.Sprintf("big%02d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	cli, err := golibrpc.Dial(d.ctx, d.sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := cli.Call(d.ctx, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": rpc.TUIName}); err != nil {
		t.Fatal(err)
	}
	seen, pages := map[string]bool{}, 0
	res, err := cli.Call(d.ctx, "inbox.watch", int64(0), int64(0))
	for {
		if err != nil {
			t.Fatalf("inbox.watch, page %d: %v", pages+1, err)
		}
		pages++
		if b, err := json.Marshal(jsonOf(res)); err != nil || len(b) >= rpc.MaxMessage {
			t.Fatalf("page %d encodes to %d bytes (%v): past the transport's %d", pages, len(b), err, rpc.MaxMessage)
		}
		m, _ := jsonOf(res).(map[string]any)
		snap, _ := m["snapshot"].(map[string]any)
		items, _ := snap["items"].([]any)
		for _, it := range items {
			id, _ := it.(map[string]any)["message"].(string)
			seen[id] = true
		}
		attention, _ := snap["attention"].([]any)
		for _, a := range attention {
			id, _ := a.(map[string]any)["session"].(string)
			seen["attention:"+id] = true
		}
		next, _ := snap["next"].(string)
		if next == "" {
			break
		}
		res, err = cli.Call(d.ctx, "inbox.watch", m["cursor"], int64(0), next)
	}
	if len(seen) != 110 || pages < 3 {
		t.Fatalf("%d of 40 attentions and 70 open requests reached the window, in %d pages; want all 110, in several", len(seen), pages)
	}
}

// TestASnapshotMeasuresMetadataThroughRPC: a page is bounded by its entries' whole wire size, not
// their text alone. A sender's codename stored at 16 KiB (as a row from before crew bounded names
// would hold it) rides on each of its 260 tiny requests: 4.2 MB of names alone, past the 4 MiB
// message. Through the daemon's socket, every page still encodes under the message and all 260
// arrive.
func TestASnapshotMeasuresMetadataThroughRPC(t *testing.T) {
	d := newCrewDaemon(t)
	c, err := readSessionContext(d.agent("gold", "agent-1", "store-1"))
	if err != nil {
		t.Fatal(err)
	}
	st := sqlitecrewmail.New(d.deps.store)
	if err := st.Write(d.ctx, func(tx crew.Tx) error {
		p, _, err := tx.Profile(c.Profile)
		if err != nil {
			return err
		}
		p.Codename = strings.Repeat("g", 16<<10)
		return tx.PutProfile(p)
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 260 {
		if _, err := d.deps.mail.Send(d.ctx, c.Credential, mail.Send{To: []string{mail.UserCodename}, Kind: mail.KindRequest,
			Subject: "s", Body: "b", Key: fmt.Sprintf("tiny%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	cli, err := golibrpc.Dial(d.ctx, d.sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := cli.Call(d.ctx, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": rpc.TUIName}); err != nil {
		t.Fatal(err)
	}
	seen, pages := map[string]bool{}, 0
	res, err := cli.Call(d.ctx, "inbox.watch", int64(0), int64(0))
	for {
		if err != nil {
			t.Fatalf("inbox.watch, page %d: %v", pages+1, err)
		}
		pages++
		if b, err := json.Marshal(jsonOf(res)); err != nil || len(b) >= rpc.MaxMessage {
			t.Fatalf("page %d encodes to %d bytes (%v): past the transport's %d", pages, len(b), err, rpc.MaxMessage)
		}
		m, _ := jsonOf(res).(map[string]any)
		snap, _ := m["snapshot"].(map[string]any)
		items, _ := snap["items"].([]any)
		for _, it := range items {
			id, _ := it.(map[string]any)["message"].(string)
			seen[id] = true
		}
		next, _ := snap["next"].(string)
		if next == "" {
			break
		}
		res, err = cli.Call(d.ctx, "inbox.watch", m["cursor"], int64(0), next)
	}
	if len(seen) != 260 || pages < 3 {
		t.Fatalf("%d of 260 requests reached the window, in %d pages; want all, in several", len(seen), pages)
	}
}

// TestTheLongPollCapIsPerConnectionAcrossReHellos (docs/decisions/crew.md#durable-mail): a
// connection holds at most two long-polls. A third is refused, and still is after the client says
// sys.hello again on the same socket, which mints it a new client token: the cap counts the
// connection, not the token. Another connection has its own.
func TestTheLongPollCapIsPerConnectionAcrossReHellos(t *testing.T) {
	d := newCrewDaemon(t)
	dial := func() *golibrpc.Client {
		t.Helper()
		cli, err := golibrpc.Dial(d.ctx, d.sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cli.Close() })
		if _, err := cli.Call(d.ctx, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": rpc.TUIName}); err != nil {
			t.Fatal(err)
		}
		return cli
	}
	busy := func(err error) bool {
		var re *golibrpc.Error
		return errors.As(err, &re) && re.Code == crew.CodeLongPollsBusy
	}
	a := dial()
	held := make(chan error, 2)
	for range 2 { // an empty journal: each waits out its timeout
		go func() { _, err := a.Call(d.ctx, "inbox.watch", int64(0), int64(5000)); held <- err }()
	}
	// both admitted, read from the limiter itself: nothing probes for a slot while they enter
	for deadline := time.Now().Add(4 * time.Second); d.deps.polls.Held() < 2; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d long-polls held, want the 2 in flight", d.deps.polls.Held())
		}
	}
	if _, err := a.Call(d.ctx, "inbox.watch", int64(0), int64(0)); !busy(err) {
		t.Fatalf("a third long-poll on the connection: %v, want rpc.long_polls_busy", err)
	}
	if _, err := a.Call(d.ctx, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": rpc.TUIName}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Call(d.ctx, "inbox.watch", int64(0), int64(0)); !busy(err) {
		t.Fatalf("a third long-poll after a re-hello on the same socket: %v, want rpc.long_polls_busy still", err)
	}
	if n := d.deps.polls.Held(); n != 2 {
		t.Fatalf("%d long-polls held after the re-hello, want the same 2", n)
	}
	if _, err := dial().Call(d.ctx, "inbox.watch", int64(0), int64(100)); err != nil {
		t.Fatalf("another connection's first long-poll: %v, want admitted", err)
	}
	select { // the two were held all along: neither answered before its timeout
	case err := <-held:
		t.Fatalf("a held long-poll answered during the checks (%v): the refusals were not the cap's", err)
	default:
	}
	for range 2 {
		if err := <-held; err != nil {
			t.Fatalf("a held long-poll: %v", err)
		}
	}
}

// An agent's bin/autodoc runs this build as `autodoc --config <file> "$@"` (tui/agent.go's
// agentWrapper), so its `crew` and `mail` commands arrive after a leading --config: they run as
// typed (the session's context names the daemon, not the config), in both of the flag's spellings.
// Any other command after --config is still a mode's.
func TestTheCommandsRunAfterTheWrappersConfig(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir())
	gold := d.agent("gold", "agent-1", "store-1")
	t.Setenv("AUTODOC_CREW_CONTEXT", gold)
	cfg := filepath.Join(t.TempDir(), "config.toml")
	for _, args := range [][]string{
		{"--config", cfg, "crew", "whoami"},
		{"--config=" + cfg, "crew", "whoami"},
		{"-config", cfg, "crew", "whoami"},
	} {
		var out, errOut bytes.Buffer
		code := run(d.ctx, args, Options{Version: "v1.2.3"}, &out, &errOut)
		if code != 0 || !strings.Contains(out.String(), `"gold"`) {
			t.Errorf("%q: exit %d, out %q, err %q; want gold's whoami", args, code, out.String(), errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := run(d.ctx, []string{"--config", cfg, "mail", "nope"}, Options{Version: "v1.2.3"}, &out, &errOut); code != 2 ||
		!strings.Contains(errOut.String(), `no command "mail nope"`) {
		t.Errorf("an unknown mail command after --config: exit %d, %q; want the crew CLI's usage", code, errOut.String())
	}
	if rest, ok := crewCommandArgs([]string{"--config", cfg, "--version"}); ok {
		t.Errorf("--version after --config was taken as a crew command: %q", rest)
	}
	if _, ok := crewCommandArgs([]string{"--config"}); ok {
		t.Error("a bare --config was taken as a crew command")
	}
}
