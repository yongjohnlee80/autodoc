package app

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// asRaw runs `autodoc <args>` as the agent whose context file is path, and answers its exit and
// stdout as printed.
func (d *crewDaemon) asRaw(path string, args ...string) (int, string) {
	d.t.Helper()
	d.t.Setenv("AUTODOC_CREW_CONTEXT", path)
	var out, errOut bytes.Buffer
	code := crewMain(d.ctx, args, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		d.t.Fatalf("%v: exit %d, %s", args, code, errOut.String())
	}
	return code, out.String()
}

// compact fails unless out is one line of JSON: what an agent reads, each byte paid for.
func compact(t *testing.T, what, out string) map[string]any {
	t.Helper()
	if strings.Count(strings.TrimSuffix(out, "\n"), "\n") != 0 || strings.Contains(out, `": `) {
		t.Errorf("%s is not printed compactly: %q", what, out)
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%s is not JSON: %q", what, out)
	}
	m, _ := v.(map[string]any)
	if m == nil {
		m = map[string]any{"list": v}
	}
	return m
}

// keys is m's keys, sorted.
func keys(m map[string]any) []string { return slices.Sorted(maps.Keys(m)) }

// TestTheMailCommandsAnswerCompactly (token items 3, 4, 8): what the agent reads of its mail costs
// only what it needs. Each command prints one line of JSON, no indent. A send's and a reply's result
// carry no body (the sender wrote it) and no sender (it is the caller), and a send's deliveries only
// their recipient and state. A received item names its delivery once, as its id: the delivery's id
// and lease aren't printed again inside it, its answer is the command with `<id>` in place of the
// id, and its message carries no sender profile beside its from. A show reads a held item again,
// the same as its receive printed it.
func TestTheMailCommandsAnswerCompactly(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir())
	gold, jarvis := d.agent("gold", "agent-1", "store-1"), d.agent("jarvis", "agent-2", "store-1")
	_, out := d.asRaw(gold, "mail", "send", "jarvis", "--kind", "request", "--subject", "review", "--body", "the-whole-diff")
	sent := compact(t, "mail send", out)
	if strings.Contains(out, "the-whole-diff") {
		t.Errorf("mail send echoes the body: %s", out)
	}
	if m, _ := sent["message"].(map[string]any); m == nil || m["id"] == nil || m["sender"] != nil || m["body"] != nil {
		t.Errorf("mail send's message: %v, want its id, and no body or sender", sent["message"])
	}
	for _, it := range sent["deliveries"].([]any) {
		if got := keys(it.(map[string]any)); !slices.Equal(got, []string{"recipient", "state"}) {
			t.Errorf("mail send's delivery has %v, want recipient and state", got)
		}
	}

	_, out = d.asRaw(jarvis, "mail", "receive")
	got := compact(t, "mail receive", out)
	ds, _ := got["deliveries"].([]any)
	if len(ds) != 1 {
		t.Fatalf("jarvis's receive: %s", out)
	}
	item := ds[0].(map[string]any)
	id, _ := item["id"].(string)
	if n := strings.Count(out, strings.SplitN(id, ":", 2)[0]); n != 1 {
		t.Errorf("the delivery's id is printed %d times, want once (its id): %s", n, out)
	}
	if k := keys(item); !slices.Equal(k, []string{"answer", "attempts", "from", "id", "lease_until", "message", "peer"}) {
		t.Errorf("a received item has %v, want answer, attempts, from, id, lease_until, message and peer (gold is a peer)", k)
	}
	if want := `autodoc mail reply <id> --body "<your reply>"`; item["answer"] != want {
		t.Errorf("a request's answer: %q, want %q", item["answer"], want)
	}
	msg, _ := item["message"].(map[string]any)
	if msg["sender"] != nil || msg["body"] != "the-whole-diff" || item["from"] != "gold" {
		t.Errorf("a received message: %v from %v, want its body, from gold, and no sender profile", msg, item["from"])
	}

	_, out = d.asRaw(jarvis, "mail", "show", id)
	shown := compact(t, "mail show", out)
	if shown["id"] != id || shown["message"].(map[string]any)["body"] != "the-whole-diff" || shown["answer"] != item["answer"] {
		t.Errorf("mail show %s: %s, want the item its receive printed", id, out)
	}

	_, out = d.asRaw(jarvis, "mail", "reply", id, "--body", "the-whole-review")
	replied := compact(t, "mail reply", out)
	if strings.Contains(out, "the-whole-review") || replied["message"].(map[string]any)["sender"] != nil {
		t.Errorf("mail reply echoes the body or the sender: %s", out)
	}
	_, out = d.asRaw(gold, "mail", "receive")
	ds = compact(t, "gold's mail receive", out)["deliveries"].([]any)
	if len(ds) != 1 || ds[0].(map[string]any)["answer"] != "autodoc mail ack <id>" {
		t.Errorf("a message's answer: %s, want `autodoc mail ack <id>`", out)
	}
}

// TestTheCrewListLeavesEndedProfilesOut (token item 5): crew list shows who can be reached or may
// come back; a profile whose session ended shows only with --all.
func TestTheCrewListLeavesEndedProfilesOut(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir())
	gold, gone := d.agent("gold", "agent-1", "store-1"), d.agent("gone", "agent-2", "store-1")
	c, err := readSessionContext(gone)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.deps.crew.Exit(d.ctx, "autodoc:aaaa", d.run, d.ep, c.Session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	codenames := func(args ...string) []string {
		_, out := d.asRaw(gold, args...)
		var names []string
		for _, e := range compact(t, strings.Join(args, " "), out)["list"].([]any) {
			names = append(names, e.(map[string]any)["codename"].(string))
		}
		return names
	}
	if got := codenames("crew", "list"); !slices.Equal(got, []string{"gold"}) {
		t.Errorf("crew list: %v, want gold alone (gone's session ended; the reserved autodoc and user have none)", got)
	}
	if got := codenames("crew", "list", "--all"); !slices.Contains(got, "gold") || !slices.Contains(got, "gone") {
		t.Errorf("crew list --all: %v, want gone listed beside gold", got)
	}
}

// TestTheMCPToolsAnswerCompactly (token items 3, 4, 5, 8): the crew MCP server's results are the
// commands' compact JSON, nothing HTML-escaped; a received item's answer is the tool's name alone,
// its id printed once; mail_show reads a held item again; crew_list leaves ended profiles out
// unless all is true.
func TestTheMCPToolsAnswerCompactly(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir())
	gold, jarvis := d.agent("gold", "agent-1", "store-1"), d.agent("jarvis", "agent-2", "store-1")
	g := d.mcp(gold)
	text, failed := g.tool("mail_send", map[string]any{"to": []string{"jarvis"}, "kind": "request", "body": "the-whole-diff"})
	if failed || strings.Contains(text, "the-whole-diff") {
		t.Errorf("mail_send: %s (error %v), want no body echoed", text, failed)
	}
	compact(t, "mail_send's result", text)
	j := d.mcp(jarvis)
	text, failed = j.tool("mail_receive", nil)
	if failed {
		t.Fatal(text)
	}
	got := compact(t, "mail_receive's result", text)
	item := got["deliveries"].([]any)[0].(map[string]any)
	id, _ := item["id"].(string)
	if item["answer"] != "mail_reply" || strings.Count(text, strings.SplitN(id, ":", 2)[0]) != 1 {
		t.Errorf("a received request: %s, want answer mail_reply and its id once", text)
	}
	text, failed = j.tool("mail_show", map[string]any{"id": id})
	if failed || !strings.Contains(text, "the-whole-diff") || compact(t, "mail_show's result", text)["answer"] != "mail_reply" {
		t.Errorf("mail_show %s: %s (error %v), want the item again", id, text, failed)
	}
	text, _ = j.tool("mail_reply", map[string]any{"id": id, "body": "<looks good>"})
	if strings.Contains(text, "looks good") {
		t.Errorf("mail_reply: %s, want no body echoed", text)
	}
	// a body as written: nothing HTML-escaped (json.Marshal writes < as \u003c), each byte paid for
	g.tool("mail_send", map[string]any{"to": []string{"jarvis"}, "body": "if a < b && c > d"})
	if text, _ = j.tool("mail_receive", nil); !strings.Contains(text, "if a < b && c > d") {
		t.Errorf("mail_receive: %s, want the body as written, nothing HTML-escaped", text)
	}

	c, err := readSessionContext(jarvis)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.deps.crew.Exit(d.ctx, "autodoc:aaaa", d.run, d.ep, c.Session, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	if text, _ := g.tool("crew_list", nil); strings.Contains(text, `"jarvis"`) {
		t.Errorf("crew_list: %s, want jarvis (ended) left out", text)
	}
	if text, _ := g.tool("crew_list", map[string]any{"all": true}); !strings.Contains(text, `"jarvis"`) {
		t.Errorf("crew_list all: %s, want jarvis too", text)
	}
}
