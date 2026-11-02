package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// mcpClient is a client of `autodoc crew mcp` run as the session whose context path names: it
// writes one request at a time and reads its answer; the server's notifications in between are kept.
type mcpClient struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan []byte
	done  chan int
	n     int
	notes []map[string]any
	// waiting is the last tool result's waiting line
	waiting string
}

func (d *crewDaemon) mcp(path string) *mcpClient {
	d.t.Helper()
	d.t.Setenv("AUTODOC_CREW_CONTEXT", path)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &mcpClient{t: d.t, in: inW, lines: make(chan []byte, 64), done: make(chan int, 1)}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for sc.Scan() {
			c.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(c.lines)
	}()
	go func() {
		code := crewMainIn(d.ctx, "", []string{"crew", "mcp"}, inR, outW, io.Discard)
		_ = outW.Close()
		c.done <- code
	}()
	d.t.Cleanup(func() { _ = inW.Close(); <-c.done })
	return c
}

// next is the server's next line within bound, as JSON; ok is false at its bound or its end.
func (c *mcpClient) next(bound time.Duration) (m map[string]any, ok bool) {
	c.t.Helper()
	select {
	case b, open := <-c.lines:
		if !open {
			return nil, false
		}
		if err := json.Unmarshal(b, &m); err != nil {
			c.t.Fatalf("the server wrote %q: %v", b, err)
		}
		return m, true
	// NOTE: a bound on a failing run only: an answer that never comes fails here rather than hanging.
	case <-time.After(bound):
		return nil, false
	}
}

// call sends method with params and answers the reply's result, or its error; notifications that
// come first are kept in notes.
func (c *mcpClient) call(method string, params any) (result map[string]any, rpcErr map[string]any) {
	c.t.Helper()
	c.n++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.n, "method": method, "params": params})
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	for {
		m, ok := c.next(30 * time.Second)
		if !ok {
			c.t.Fatalf("%s: no answer", method)
		}
		if _, isNote := m["method"]; isNote && m["id"] == nil {
			c.notes = append(c.notes, m)
			continue
		}
		if m["id"] != float64(c.n) {
			c.t.Fatalf("%s: answer %v, want id %d", method, m, c.n)
		}
		result, _ = m["result"].(map[string]any)
		rpcErr, _ = m["error"].(map[string]any)
		return result, rpcErr
	}
}

// note waits within bound for the server's next notification, which nothing asked for.
func (c *mcpClient) note(bound time.Duration) (map[string]any, bool) {
	c.t.Helper()
	if len(c.notes) > 0 {
		m := c.notes[0]
		c.notes = c.notes[1:]
		return m, true
	}
	m, ok := c.next(bound)
	if ok && m["id"] != nil {
		c.t.Fatalf("an answer nobody asked for: %v", m)
	}
	return m, ok
}

// tool calls a tool and answers its text and whether it failed.
func (c *mcpClient) tool(name string, args map[string]any) (text string, isError bool) {
	c.t.Helper()
	res, rpcErr := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if rpcErr != nil {
		c.t.Fatalf("tools/call %s: %v", name, rpcErr)
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		c.t.Fatalf("tools/call %s: content %v", name, res)
	}
	text, _ = content[0].(map[string]any)["text"].(string)
	c.waiting, _ = content[len(content)-1].(map[string]any)["text"].(string)
	isError, _ = res["isError"].(bool)
	return text, isError
}

// received is a mail_receive's items.
func (c *mcpClient) received() []map[string]any {
	c.t.Helper()
	text, failed := c.tool("mail_receive", nil)
	if failed {
		c.t.Fatalf("mail_receive: %s", text)
	}
	var res struct {
		Deliveries []map[string]any `json:"deliveries"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		c.t.Fatalf("mail_receive's text %q: %v", text, err)
	}
	return res.Deliveries
}

// TestTheCrewMCPServerIsTheMailCommands (docs/decisions/crew.md#the-crew-mcp-server): `autodoc crew
// mcp` answers MCP's handshake and lists the four tools of the table hosts allow; each tool acts as
// the session the server's context names, through the CLI's own commands: a send arrives, a
// received item's answer names the tool call that answers it, the reply that call makes acks the
// request and reaches the sender, and an ack acts once. A refused call is the tool's error, the
// daemon's code in it, never the server's; an unknown method is JSON-RPC's.
func TestTheCrewMCPServerIsTheMailCommands(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir()) // no context of its own: the env's is the session
	gold, jarvis := d.agent("gold", "agent-1", "store-1"), d.agent("jarvis", "agent-2", "store-1")
	g := d.mcp(gold)
	hello, rpcErr := g.call("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test"}})
	if rpcErr != nil || hello["protocolVersion"] != "2025-11-25" || hello["serverInfo"].(map[string]any)["name"] != MCPServerName {
		t.Fatalf("initialize: %v %v", hello, rpcErr)
	}
	if _, rpcErr := g.call("server/discover", map[string]any{}); rpcErr == nil || rpcErr["code"] != float64(-32601) {
		t.Fatalf("an unknown method: %v, want method not found", rpcErr)
	}
	list, _ := g.call("tools/list", nil)
	var names []string
	for _, tool := range list["tools"].([]any) {
		names = append(names, tool.(map[string]any)["name"].(string))
	}
	if !slices.Equal(names, MCPTools()) || !slices.Equal(names, []string{"mail_receive", "mail_ack", "mail_reply", "mail_renew", "mail_show", "crew_whoami", "crew_list", "mail_send"}) {
		t.Fatalf("tools/list: %v, want the table %v", names, MCPTools())
	}
	if text, failed := g.tool("crew_whoami", nil); failed || !strings.Contains(text, `"codename":"gold"`) {
		t.Fatalf("gold's crew_whoami: %s (error %v)", text, failed)
	}
	if text, failed := g.tool("crew_list", nil); failed || !strings.Contains(text, `"jarvis"`) {
		t.Fatalf("gold's crew_list: %s (error %v)", text, failed)
	}
	if text, failed := g.tool("mail_send", map[string]any{"to": []string{"jarvis"}, "kind": "request", "subject": "review", "body": "-the diff, please"}); failed {
		t.Fatalf("gold's mail_send: %s", text)
	}
	if text, failed := g.tool("mail_send", map[string]any{"to": []string{"jarvis"}, "body": "fyi"}); failed {
		t.Fatalf("gold's second mail_send: %s", text)
	}

	j := d.mcp(jarvis)
	items := j.received()
	if len(items) != 2 {
		t.Fatalf("jarvis's mail_receive: %v, want the request and the message", items)
	}
	var req, msg map[string]any
	for _, it := range items {
		if it["message"].(map[string]any)["kind"] == "request" {
			req = it
		} else {
			msg = it
		}
	}
	if req == nil || msg == nil || req["message"].(map[string]any)["body"] != "-the diff, please" {
		t.Fatalf("jarvis's items: %v", items)
	}
	rid, mid := req["id"].(string), msg["id"].(string)
	if req["answer"] != "mail_reply" || msg["answer"] != "mail_ack" {
		t.Fatalf("the answers: %q and %q, want mail_reply and mail_ack", req["answer"], msg["answer"])
	}
	if text, failed := j.tool("mail_renew", map[string]any{"id": rid}); failed {
		t.Fatalf("jarvis's mail_renew: %s", text)
	}
	if text, failed := j.tool("mail_reply", map[string]any{"id": rid, "body": "-looks good"}); failed {
		t.Fatalf("jarvis's mail_reply: %s", text)
	}
	if text, failed := j.tool("mail_ack", map[string]any{"id": mid}); failed {
		t.Fatalf("jarvis's mail_ack: %s", text)
	}
	if again := j.received(); len(again) != 0 {
		t.Fatalf("after the answers, jarvis's mail_receive: %v; want none", again)
	}
	// a refused call is the tool's error, with the daemon's code
	if text, failed := j.tool("mail_ack", map[string]any{"id": mid + "x"}); !failed || !strings.Contains(text, `"code":-32113`) {
		t.Fatalf("an ack under a lease jarvis doesn't hold: %q (error %v), want the tool's error, mail.lease_lost", text, failed)
	}
	if text, failed := j.tool("mail_reply", map[string]any{"id": rid}); !failed || !strings.Contains(text, "body is required") {
		t.Fatalf("a reply with no body: %q (error %v), want the tool's error naming body", text, failed)
	}
	if text, failed := j.tool("mail_delete", nil); !failed || !strings.Contains(text, "no tool mail_delete") {
		t.Fatalf("an unknown tool: %q (error %v)", text, failed)
	}

	back := g.received()
	if len(back) != 1 || back[0]["message"].(map[string]any)["body"] != "-looks good" || back[0]["message"].(map[string]any)["kind"] != "response" {
		t.Fatalf("gold's mail_receive of the reply: %v", back)
	}
}

// TestTheCrewMCPServerSaysWhenMailWaits (docs/decisions/crew.md#the-crew-mcp-server): every tool
// result ends with how much mail the session may receive, and when some waits, to receive it once
// the step is done; initialize's instructions say to act on it. Once initialized, the server tells
// the client of new mail as it arrives (notifications/message), with no tool call: once per arrival,
// and again for mail that comes after a receive.
func TestTheCrewMCPServerSaysWhenMailWaits(t *testing.T) {
	d := newCrewDaemon(t)
	// the daemon's scheduler, as serve makes it: its Watch is what ends mail.wait when mail arrives
	_, stopScheduler := startScheduler(d.ctx, d.deps.crew, nil, logger.Nop{})
	t.Cleanup(stopScheduler)
	t.Chdir(t.TempDir())
	gold, jarvis := d.agent("gold", "agent-1", "store-1"), d.agent("jarvis", "agent-2", "store-1")
	j := d.mcp(jarvis)
	hello, _ := j.call("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if ins, _ := hello["instructions"].(string); !strings.Contains(ins, "waiting above 0") || !strings.Contains(ins, "end of each task") {
		t.Fatalf("initialize's instructions: %q", ins)
	}
	if _, failed := j.tool("mail_ack", map[string]any{"id": "none:none"}); !failed || j.waiting != "waiting: 0" {
		t.Fatalf("a result with no mail waiting ends %q, want waiting: 0", j.waiting)
	}
	if _, ok := j.note(500 * time.Millisecond); ok {
		t.Fatal("a notice with no mail")
	}
	// mail arrives while jarvis works: the server says so, unasked
	if code, _, errOut := d.as(gold, "", "mail", "send", "jarvis", "--body", "one"); code != 0 {
		t.Fatalf("gold's send: %s", errOut)
	}
	n, ok := j.note(15 * time.Second)
	if !ok || n["method"] != "notifications/message" || n["params"].(map[string]any)["data"] != mcpWaitingNote {
		t.Fatalf("after mail arrived: %v %v, want the notice", n, ok)
	}
	if code, _, errOut := d.as(gold, "", "mail", "send", "jarvis", "--body", "two"); code != 0 {
		t.Fatalf("gold's second send: %s", errOut)
	}
	// the next tool result counts both
	text, _ := j.tool("mail_send", map[string]any{"to": []string{"gold"}, "body": "working on it"})
	if j.waiting != "waiting: 2. "+mcpWaitingNote {
		t.Fatalf("a result with two waiting ends %q (%s)", j.waiting, text)
	}
	items := j.received()
	if len(items) != 2 || j.waiting != "waiting: 0" {
		t.Fatalf("mail_receive: %d items, ending %q; want both, waiting 0", len(items), j.waiting)
	}
	for _, it := range items {
		if text, failed := j.tool("mail_ack", map[string]any{"id": it["id"]}); failed {
			t.Fatalf("ack: %s", text)
		}
	}
	// mail after a receive is told of again
	for len(j.notes) > 0 { // the second arrival's notice, if the watch saw it apart from the first
		_, _ = j.note(0)
	}
	if code, _, errOut := d.as(gold, "", "mail", "send", "jarvis", "--body", "three"); code != 0 {
		t.Fatalf("gold's third send: %s", errOut)
	}
	if n, ok := j.note(45 * time.Second); !ok || n["params"].(map[string]any)["data"] != mcpWaitingNote {
		t.Fatalf("mail after a receive: %v %v, want the notice again", n, ok)
	}
}

// TestTheCrewMCPServerEndsWithItsInput: closing the server's input ends it promptly, its mail watch
// too, even while that watch is in a 25-second long-poll: nothing it started outlives it.
func TestTheCrewMCPServerEndsWithItsInput(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir())
	jarvis := d.agent("jarvis", "agent-2", "store-1")
	j := d.mcp(jarvis)
	j.call("initialize", map[string]any{})
	j.call("ping", nil)
	time.Sleep(300 * time.Millisecond) // the watch is in its long-poll by now
	_ = j.in.Close()
	select {
	case code := <-j.done:
		j.done <- code // for the cleanup
		if code != 0 {
			t.Fatalf("the server ended with %d", code)
		}
	// NOTE: the bound is the property: a watch left in its long-poll would hold the server 25 s.
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not end within 5 s of its input closing: its mail watch outlives it")
	}
}

// TestTheMCPToolsTakeWhatTheGuideSays: every tool's schema takes exactly the arguments
// crew.MCPToolArgs says, required as it says, in its order of tools; and what the agent is told
// (the guide's line a tool, the wake notice, the waiting note, the server's instructions) says them
// too, so an agent never needs a schema, and the two can't drift.
func TestTheMCPToolsTakeWhatTheGuideSays(t *testing.T) {
	if len(crewMCPTools) != len(crew.MCPToolArgs) {
		t.Fatalf("%d tools, %d in crew.MCPToolArgs", len(crewMCPTools), len(crew.MCPToolArgs))
	}
	guide := crew.GuideOf("agy").Markdown
	for i, tool := range crewMCPTools {
		want := crew.MCPToolArgs[i]
		if tool.Name != want.Name {
			t.Errorf("tool %d is %s, crew.MCPToolArgs says %s", i, tool.Name, want.Name)
			continue
		}
		var props []string
		for p := range tool.Schema["properties"].(map[string]any) {
			props = append(props, p)
		}
		required, _ := tool.Schema["required"].([]string)
		all := append(slices.Clone(want.Required), want.Optional...)
		slices.Sort(props)
		slices.Sort(all)
		if !slices.Equal(props, all) || !slices.Equal(required, want.Required) {
			t.Errorf("%s's schema takes %v, requires %v; crew.MCPToolArgs says %v, requiring %v", tool.Name, props, required, all, want.Required)
		}
		if line := fmt.Sprintf("\n  - `%s`: %s\n", tool.Name, want.Says()); !strings.Contains(guide, line) {
			t.Errorf("the guide lacks %s's arguments line %q", tool.Name, line)
		}
		if !strings.Contains(mcpInstructions, "`"+tool.Name+"`: "+want.Says()) {
			t.Errorf("the server's instructions lack %s's arguments: %q", tool.Name, mcpInstructions)
		}
	}
	if recv, _ := crew.MCPToolArgsOf("mail_receive"); recv.Says() != "none" {
		t.Fatalf("mail_receive takes %s; the notices say it takes no arguments", recv.Says())
	}
	for what, text := range map[string]string{"the wake notice": crew.WakeNoticeMCP, "the waiting note": mcpWaitingNote} {
		if !strings.Contains(text, "call mail_receive, a tool of the autodoc MCP server, with no arguments,") {
			t.Errorf("%s doesn't say mail_receive takes no arguments: %q", what, text)
		}
	}
}

// TestThePerEventTextsKeepTheirSize (docs/decisions/crew.md#the-crew-mcp-server): the texts an agent
// reads on every wake or tool result are pinned to their size in bytes, since each byte is paid for
// the life of every session; changing one means changing its size here, on purpose.
func TestThePerEventTextsKeepTheirSize(t *testing.T) {
	for _, c := range []struct {
		what, text string
		size       int
	}{
		{"the CLI wake notice (crew.WakeNotice), per wake", crew.WakeNotice, 58},
		{"the MCP wake notice (crew.WakeNoticeMCP), per wake", crew.WakeNoticeMCP, 125},
		{"the CLI wake notice naming one sender, per wake", crew.NoticeFor(crew.MailViaCLI, []crew.Sender{{From: "gold-man", Kind: "claude", Count: 1}}, false), 84},
		{"the MCP wake notice naming one sender, per wake", crew.NoticeFor(crew.MailViaMCP, []crew.Sender{{From: "gold-man", Kind: "claude", Count: 1}}, false), 151},
		{"the CLI wake notice at its cap of senders, per wake", crew.NoticeFor(crew.MailViaCLI, []crew.Sender{{From: "gold-man", Kind: "claude", Count: 2},
			{From: "user", Count: 1}, {From: "kimmy-vision", Kind: "claude", Count: 1}, {From: "lector", Kind: "codex", Count: 12}}, false), 140},
		{"the waiting line with nothing waiting, per MCP tool result", waitingLine(0), 10},
		{"the waiting line with mail waiting, per MCP tool result", waitingLine(1), 132},
	} {
		if len(c.text) != c.size {
			t.Errorf("%s is %d bytes, pinned at %d: %q", c.what, len(c.text), c.size, c.text)
		}
	}
}
