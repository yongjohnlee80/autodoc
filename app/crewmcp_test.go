package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
)

// mcpClient is a client of `autodoc crew mcp` run as the session whose context path names: it
// writes one request at a time and reads its answer.
type mcpClient struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Scanner
	done chan int
	n    int
}

func (d *crewDaemon) mcp(path string) *mcpClient {
	d.t.Helper()
	d.t.Setenv("AUTODOC_CREW_CONTEXT", path)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &mcpClient{t: d.t, in: inW, out: bufio.NewScanner(outR), done: make(chan int, 1)}
	c.out.Buffer(make([]byte, 64<<10), 4<<20)
	go func() {
		code := crewMainIn(d.ctx, "", []string{"crew", "mcp"}, inR, outW, io.Discard)
		_ = outW.Close()
		c.done <- code
	}()
	d.t.Cleanup(func() { _ = inW.Close(); <-c.done })
	return c
}

// call sends method with params and answers the reply's result, or its error.
func (c *mcpClient) call(method string, params any) (result map[string]any, rpcErr map[string]any) {
	c.t.Helper()
	c.n++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.n, "method": method, "params": params})
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	if !c.out.Scan() {
		c.t.Fatalf("%s: no answer (%v)", method, c.out.Err())
	}
	var m struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(c.out.Bytes(), &m); err != nil || m.ID != c.n {
		c.t.Fatalf("%s: answer %s (%v), want id %d", method, c.out.Bytes(), err, c.n)
	}
	return m.Result, m.Error
}

// tool calls a tool and answers its text and whether it failed.
func (c *mcpClient) tool(name string, args map[string]any) (text string, isError bool) {
	c.t.Helper()
	res, rpcErr := c.call("tools/call", map[string]any{"name": name, "arguments": args})
	if rpcErr != nil {
		c.t.Fatalf("tools/call %s: %v", name, rpcErr)
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		c.t.Fatalf("tools/call %s: content %v", name, res)
	}
	text, _ = content[0].(map[string]any)["text"].(string)
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
	if !slices.Equal(names, MCPTools()) || !slices.Equal(names, []string{"mail_receive", "mail_ack", "mail_reply", "mail_send"}) {
		t.Fatalf("tools/list: %v, want the table %v", names, MCPTools())
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
	if want := fmt.Sprintf(`mail_reply {"id": %q, "body": "<your reply>"}`, rid); req["answer"] != want {
		t.Fatalf("the request's answer: %q, want %q", req["answer"], want)
	}
	if want := fmt.Sprintf(`mail_ack {"id": %q}`, mid); msg["answer"] != want {
		t.Fatalf("the message's answer: %q, want %q", msg["answer"], want)
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
