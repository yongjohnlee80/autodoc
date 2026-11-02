package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/internal/client"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// assignRig is a crew daemon with a todo store, one task in it, and two agents, gold and jarvis;
// at is the store's root, a registered one (the daemon serves no workspace), so its store is
// writable.
type assignRig struct {
	*crewDaemon
	gold, jarvis string // their context files
	at           todo.At
	task         todo.Task
	store        string // the store's id
}

func newAssignRig(t *testing.T) *assignRig {
	t.Helper()
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir())
	r := &assignRig{crewDaemon: d, gold: d.agent("gold", "agent-1", "store-1"), jarvis: d.agent("jarvis", "agent-2", "store-1"),
		at: todo.At{Root: t.TempDir()}}
	if _, err := d.deps.todos.Register(d.ctx, r.at.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := d.deps.todos.Scaffold(d.ctx, r.at); err != nil {
		t.Fatal(err)
	}
	task, err := d.deps.todos.Add(d.ctx, r.at, todo.Spec{Title: "Ship"}, "")
	if err != nil {
		t.Fatal(err)
	}
	r.task = task
	stores, err := d.deps.todos.Stores(d.ctx)
	if err != nil || len(stores) != 1 {
		t.Fatalf("stores %+v, %v", stores, err)
	}
	r.store = stores[0].ID
	return r
}

// cli calls method as `autodoc --call` does, its hello said by cliWho, with $AUTODOC_CREW_CONTEXT
// set to context ("" for none): what the call answered, and the text of its answer or error.
func (r *assignRig) cli(context, method string, params ...any) (any, string) {
	r.t.Helper()
	r.t.Setenv("AUTODOC_CREW_CONTEXT", context)
	s := client.NewSession(r.sock, nil).As(rpc.CLIName).WithInfo(cliWho())
	if err := s.Connect(r.ctx); err != nil {
		r.t.Fatal(err)
	}
	defer s.Close()
	v, err := s.Call(r.ctx, method, params...)
	b, _ := json.Marshal(jsonOf(v))
	return v, fmt.Sprint(string(b), err)
}

// assign assigns the task to jarvis as the CLI run with context does, and answers the assign's
// change as todos.changes gives it, and every text the call and the read answered.
func (r *assignRig) assign(context string) (todo.Change, []string) {
	r.t.Helper()
	cur, err := r.deps.todos.Get(r.ctx, r.at, r.task.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	_, said := r.cli(context, "todos.assign", map[string]any{"root": r.at.Root}, r.task.ID, cur.Rev, "autodoc:jarvis", "Take it.", "")
	_, read := r.cli("", "todos.changes", map[string]any{"root": r.at.Root}, "a-reader", int64(0))
	page, err := r.deps.todos.Changes(r.ctx, r.at, "another-reader", 0)
	if err != nil || len(page.Changes) == 0 {
		r.t.Fatalf("changes %+v, %v", page, err)
	}
	c := page.Changes[len(page.Changes)-1]
	if c.Kind != todo.KindAssign {
		r.t.Fatalf("the last change is %+v, want the assign (the call said %s)", c, said)
	}
	return c, []string{said, read}
}

// forward is the window's inbox.forward of change c to jarvis, keyed by c, as tui's feed sends it.
func (r *assignRig) forward(c todo.Change) string {
	r.t.Helper()
	_, said := r.cli("", "inbox.forward", "autodoc:aaaa", r.run, r.ep, r.store, c.Seq, c.ID, []any{"jarvis"},
		"You've been assigned a new task.", fmt.Sprintf("todo-%s-%d", r.store, c.Seq), "message", "[todos] task assigned to you: Ship")
	return said
}

// received is jarvis's deliveries, each as `autodoc mail receive` prints it.
func (r *assignRig) received() []map[string]any {
	r.t.Helper()
	_, out := r.asRaw(r.jarvis, "mail", "receive")
	var got []map[string]any
	for _, it := range compact(r.t, "mail receive", out)["deliveries"].([]any) {
		got = append(got, it.(map[string]any))
	}
	return got
}

// An assignment an agent made, its CLI run with the agent's crew context
// (docs/decisions/todos.md#an-assignment-says-who-made-it), records the agent as its maker, from
// the credential its hello carried, which is stored nowhere and answered nowhere; the window's
// forward of it reaches the assignee from that agent, with the peer marker, never from the user. The
// same forward again, by its key, delivers nothing more.
func TestAnAgentsAssignmentIsForwardedFromTheAgent(t *testing.T) {
	r := newAssignRig(t)
	c, texts := r.assign(r.gold)
	if c.By != "gold@autodoc:aaaa" {
		t.Fatalf("the assign's maker %q, want gold@autodoc:aaaa", c.By)
	}
	texts = append(texts, r.forward(c))
	got := r.received()
	if len(got) != 1 || got[0]["from"] != "gold" || got[0]["peer"] != peerNote {
		t.Fatalf("jarvis received %v, want the assignment from gold with the peer marker", got)
	}
	texts = append(texts, r.forward(c))
	if again := r.received(); len(again) != 0 {
		t.Errorf("the forward again, by its key: %v, want nothing more", again)
	}
	secret := credentialOf(t, r.gold)
	for _, s := range texts {
		if strings.Contains(s, secret) {
			t.Errorf("the credential is out: %s", s)
		}
	}
}

// An assignment made with no crew context (the user's own shell, or a client older than the caller's
// proof: neither sends a crew key) names no maker, and its forward is the user's, with no peer
// marker, as inbox.send's is.
func TestAnAssignmentWithNoCrewContextIsTheUsers(t *testing.T) {
	r := newAssignRig(t)
	c, _ := r.assign("")
	if c.By != "" {
		t.Fatalf("the assign's maker %q, want none: the user's surface", c.By)
	}
	r.forward(c)
	got := r.received()
	if len(got) != 1 || got[0]["from"] != "user" || got[0]["peer"] != nil {
		t.Fatalf("jarvis received %v, want the assignment from the user, no peer marker", got)
	}
}

// A crew context whose credential doesn't verify, or that can't be read, is an agent's all the same,
// never the user's: its assign records the unverified marker, and its forward reaches the assignee
// from autodoc (the daemon), with the peer marker and a line saying the maker couldn't be verified.
// The credential it carried is in no answer.
func TestAnUnverifiedAssignmentIsNeverTheUsers(t *testing.T) {
	for _, forge := range []bool{true, false} {
		r := newAssignRig(t)
		context := filepath.Join(t.TempDir(), "no-such-context")
		if forge {
			var err error
			context, err = crew.WriteContext(t.TempDir(), sessionContext{Store: "store-1", Socket: r.sock, Scope: "autodoc:aaaa",
				Profile: "p-gold", Session: "s-gold", Credential: crew.Credential("s-gold", "forged-secret-text-0123456789abcdef"), Kind: "claude"})
			if err != nil {
				t.Fatal(err)
			}
		}
		c, texts := r.assign(context)
		if c.By != module.CallerUnverified {
			t.Fatalf("forged %v: the assign's maker %q, want %q", forge, c.By, module.CallerUnverified)
		}
		texts = append(texts, r.forward(c))
		got := r.received()
		if len(got) != 1 || got[0]["from"] != "autodoc" || got[0]["peer"] != peerNote {
			t.Fatalf("forged %v: jarvis received %v, want the assignment from autodoc with the peer marker", forge, got)
		}
		body, _ := got[0]["message"].(map[string]any)["body"].(string)
		if !strings.Contains(body, "couldn't be verified") {
			t.Errorf("forged %v: the body doesn't say its maker couldn't be verified:\n%s", forge, body)
		}
		for _, s := range texts {
			if strings.Contains(s, "forged-secret-text") {
				t.Errorf("the credential is out: %s", s)
			}
		}
	}
}

// credentialOf is the secret half of the credential in the context file at path.
func credentialOf(t *testing.T, path string) string {
	t.Helper()
	c, err := readSessionContext(path)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, _ := strings.Cut(c.Credential, ".")
	return secret
}
