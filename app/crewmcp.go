package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// `autodoc crew mcp` (docs/decisions/crew.md#the-crew-mcp-server): the crew's mail as MCP tools, on
// stdin and stdout, for an agent CLI that runs it as a stdio MCP server, an agy agent from its
// folder's .agents/mcp_config.json. Each tool is the `autodoc mail` command of its name, run the
// one way the CLI runs it (runCrewCommand), as the session whose context the server's environment
// or working folder names: no meaning of its own, and no id an agent types into a shell command.

// mcpProtocol is the MCP revision the server answers with when the client names none; mcpTools is
// the tool table's own revision, its serverInfo version.
const (
	mcpProtocol = "2025-06-18"
	mcpTools    = "1"
)

// mcpTool is one tool: its name, what it does, its input schema, and the `autodoc mail` command
// and arguments a call of it runs.
type mcpTool struct {
	Name        string
	Description string
	Schema      map[string]any
	args        func(in map[string]any) (command string, args []string, err error)
}

// MCPTools are the crew MCP server's tools, by name: crew.MCPToolNames, the one list a host's setup
// spells its per-tool allow rules from (agy's mcp(autodoc/<tool>), Claude's mcp__autodoc__<tool>),
// which the server's own table (crewMCPTools) is held to by a cell.
func MCPTools() []string { return crew.MCPToolNames() }

// MCPServerName is the name hosts register the crew MCP server under (crew.MCPServerName).
const MCPServerName = crew.MCPServerName

func strArg(in map[string]any, name string, required bool) (string, error) {
	v, ok := in[name]
	if !ok || v == nil {
		if required {
			return "", fmt.Errorf("%s is required", name)
		}
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	if required && s == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return s, nil
}

func object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// toolSchema is tool name's input schema: props, required as crew.MCPToolArgs says (which the guide,
// the notice and the instructions say too), so what the agent is told and what the server takes
// can't drift; TestTheMCPToolsTakeWhatTheGuideSays holds props to it as well.
func toolSchema(name string, props map[string]any) map[string]any {
	t, _ := crew.MCPToolArgsOf(name)
	return object(props, t.Required...)
}

var (
	idProp      = map[string]any{"type": "string", "description": "the item's id, exactly as mail_receive gave it"}
	bodyProp    = map[string]any{"type": "string", "description": "the text, at most 64 KiB"}
	subjectProp = map[string]any{"type": "string", "description": "a subject line (optional)"}
)

var crewMCPTools = []mcpTool{
	{Name: "mail_receive", Description: "Lease your next mail deliveries, at most 8; call it again until more is false. " +
		"Each item's answer names the tool and arguments that answer it.",
		Schema: toolSchema("mail_receive", map[string]any{}),
		args:   func(map[string]any) (string, []string, error) { return "mail receive", nil, nil }},
	{Name: "mail_ack", Description: "Done with a delivery (a message or an event), by the id mail_receive gave.",
		Schema: toolSchema("mail_ack", map[string]any{"id": idProp}),
		args: func(in map[string]any) (string, []string, error) {
			id, err := strArg(in, "id", true)
			return "mail ack", []string{"--", id}, err
		}},
	{Name: "mail_reply", Description: "Answer a request by the id mail_receive gave; it acks the delivery too, and is safe to repeat.",
		Schema: toolSchema("mail_reply", map[string]any{"id": idProp, "body": bodyProp, "subject": subjectProp}),
		args: func(in map[string]any) (string, []string, error) {
			id, err := strArg(in, "id", true)
			if err != nil {
				return "", nil, err
			}
			body, err := strArg(in, "body", true)
			if err != nil {
				return "", nil, err
			}
			subject, err := strArg(in, "subject", false)
			return "mail reply", []string{"--body=" + body, "--subject=" + subject, "--", id}, err
		}},
	{Name: "mail_renew", Description: "Keep a delivery 10 more minutes, for long work, by the id mail_receive gave; reply or ack when it's done.",
		Schema: toolSchema("mail_renew", map[string]any{"id": idProp}),
		args: func(in map[string]any) (string, []string, error) {
			id, err := strArg(in, "id", true)
			return "mail renew", []string{"--", id}, err
		}},
	{Name: "crew_whoami", Description: "Who you are in the crew: your codename, scope, session and store.",
		Schema: toolSchema("crew_whoami", map[string]any{}),
		args:   func(map[string]any) (string, []string, error) { return "crew whoami", nil, nil }},
	{Name: "crew_list", Description: "Every crew member's codename, whether it's live, and whether a wake reaches it.",
		Schema: toolSchema("crew_list", map[string]any{}),
		args:   func(map[string]any) (string, []string, error) { return "crew list", nil, nil }},
	{Name: "mail_send", Description: "Send mail: to a codename, name@scope, user (the person you work for) or session:<id>. " +
		"kind request asks for an answer, which wakes you when it comes.",
		Schema: toolSchema("mail_send", map[string]any{
			"to":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "description": "the recipients"},
			"body":    bodyProp,
			"subject": subjectProp,
			"kind":    map[string]any{"type": "string", "enum": []string{mail.KindMessage, mail.KindRequest}, "description": "message (the default) or request"},
		}),
		args: func(in map[string]any) (string, []string, error) {
			raw, _ := in["to"].([]any)
			if len(raw) == 0 {
				return "", nil, errors.New("to is required: at least one recipient")
			}
			body, err := strArg(in, "body", true)
			if err != nil {
				return "", nil, err
			}
			subject, err := strArg(in, "subject", false)
			if err != nil {
				return "", nil, err
			}
			kind, err := strArg(in, "kind", false)
			if err != nil {
				return "", nil, err
			}
			args := []string{"--body=" + body, "--subject=" + subject}
			if kind != "" {
				args = append(args, "--kind="+kind)
			}
			args = append(args, "--")
			for _, v := range raw {
				s, ok := v.(string)
				if !ok || s == "" {
					return "", nil, errors.New("to holds a recipient that is not a name")
				}
				args = append(args, s)
			}
			return "mail send", args, nil
		}},
}

// toolAnswers rewrites each received item's answer, the CLI's command, as the tool call that
// answers it: an agent on MCP calls a tool, and types no command.
func toolAnswers(v any) any {
	res, ok := v.(map[string]any)
	if !ok {
		return v
	}
	items, _ := res["deliveries"].([]any)
	for _, it := range items {
		item, ok := it.(map[string]any)
		if !ok {
			continue
		}
		id, _ := item["id"].(string)
		if m, _ := item["message"].(map[string]any); m != nil && m["kind"] == mail.KindRequest {
			item["answer"] = fmt.Sprintf(`mail_reply {"id": %q, "body": "<your reply>"}`, id)
		} else {
			item["answer"] = fmt.Sprintf(`mail_ack {"id": %q}`, id)
		}
	}
	return res
}

// mcpRequest is one JSON-RPC message from the client; a notification has no id.
type mcpRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// mcpInstructions is what initialize tells the client about the server: how mail reaches the agent,
// when to look for it (docs/decisions/crew.md#the-crew-mcp-server), and each tool's arguments.
var mcpInstructions = "AutoDoc's crew mail. When AutoDoc's notice wakes you, call mail_receive until more is false, " +
	"then answer each item with the tool its answer names. Every tool's result says how much mail is waiting: " +
	"whenever it shows waiting above 0, call mail_receive once the current step is done, and call it at the end of each task. " +
	crew.MCPArgumentsSentence()

// mcpWaitingNote is the line a tool result carries when mail is waiting, and the push notice's text.
const mcpWaitingNote = "AutoDoc mail is waiting: call mail_receive, a tool of the autodoc MCP server, with no arguments, once this step is done."

// mcpRecheck bounds how long the push waits before looking again while mail sits unreceived: the
// daemon's mail.wait answers at once while any is waiting, so it can't wait for the next.
const mcpRecheck = 30 * time.Second

// crewMCPMain runs `autodoc crew mcp`: newline-delimited JSON-RPC 2.0 on stdin and stdout, one
// request at a time, until stdin closes. A tool's failure is its result (isError, the daemon's or
// the argument's message), never the server's. Once initialized, it also watches the session's
// mail (mailWatch) and tells the client when new mail arrives (notifications/message); that
// watch ends with the server, which returns only once it has.
func crewMCPMain(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) int {
	// the server is one session's: the context its start names (the env's path, else its folder's),
	// read again for each call as the CLI reads it, never another the process's env names later
	envPath, cwd := os.Getenv("AUTODOC_CREW_CONTEXT"), ""
	if wd, err := os.Getwd(); err == nil {
		cwd = wd
	}
	session := func() (sessionContext, error) { return findSessionContext(envPath, cwd) }
	ctx, cancel := context.WithCancel(ctx)
	var watching sync.WaitGroup
	defer func() { cancel(); watching.Wait() }()
	in := bufio.NewScanner(stdin)
	in.Buffer(make([]byte, 64<<10), 4<<20)
	out := bufio.NewWriter(stdout)
	var writing sync.Mutex // the answers and the watch's notices share stdout
	write := func(m map[string]any) {
		b, _ := json.Marshal(m)
		writing.Lock()
		defer writing.Unlock()
		_, _ = out.Write(append(b, '\n'))
		_ = out.Flush()
	}
	say := func(id json.RawMessage, result any, rpcErr map[string]any) {
		m := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcErr != nil {
			m["error"] = rpcErr
		} else {
			m["result"] = result
		}
		write(m)
	}
	acted := make(chan struct{}, 1) // a tool ran: the watch looks again at once
	for in.Scan() {
		var req mcpRequest
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			say(json.RawMessage("null"), nil, map[string]any{"code": -32700, "message": "parse error"})
			continue
		}
		if len(req.ID) == 0 { // a notification: nothing answers it
			continue
		}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			version := p.ProtocolVersion
			if version == "" {
				version = mcpProtocol
			}
			say(req.ID, map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{}, "logging": map[string]any{}},
				"serverInfo":      map[string]any{"name": MCPServerName, "version": mcpTools},
				"instructions":    mcpInstructions,
			}, nil)
			watching.Add(1)
			go func() {
				defer watching.Done()
				mailWatch(ctx, session, acted, func() {
					write(map[string]any{"jsonrpc": "2.0", "method": "notifications/message",
						"params": map[string]any{"level": "info", "logger": MCPServerName, "data": mcpWaitingNote}})
				})
			}()
		case "ping":
			say(req.ID, map[string]any{}, nil)
		case "tools/list":
			tools := make([]map[string]any, 0, len(crewMCPTools))
			for _, t := range crewMCPTools {
				tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
			}
			say(req.ID, map[string]any{"tools": tools}, nil)
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				say(req.ID, nil, map[string]any{"code": -32602, "message": "invalid params"})
				continue
			}
			res := callMCPTool(ctx, p.Name, p.Arguments, session)
			if n, ok := mailWaiting(ctx, session); ok {
				res["content"] = append(res["content"].([]map[string]any), map[string]any{"type": "text", "text": waitingLine(n)})
			}
			say(req.ID, res, nil)
			select {
			case acted <- struct{}{}:
			default:
			}
		default:
			say(req.ID, nil, map[string]any{"code": -32601, "message": "method not found: " + req.Method})
		}
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(stderr, "autodoc crew mcp:", err)
		return 1
	}
	return 0
}

// waitingLine is a tool result's last line: how much mail is waiting, and when there is some, to
// receive it once the current step is done.
func waitingLine(n int) string {
	if n > 0 {
		return fmt.Sprintf("waiting: %d. %s", n, mcpWaitingNote)
	}
	return "waiting: 0"
}

// mailWaiting is how much mail the session may receive now (mail.inbox's count: it leases
// nothing), and false when it can't be counted (no session, no daemon).
func mailWaiting(ctx context.Context, session func() (sessionContext, error)) (int, bool) {
	c, err := session()
	if err != nil {
		return 0, false
	}
	res, err := crewCall(ctx, c, "mail.inbox", []any{c.Credential, int64(0)})
	if err != nil {
		return 0, false
	}
	m, _ := jsonOf(res).(map[string]any)
	return countOf(m["unread"])
}

// countOf is a count the daemon answered: msgpack's integer, of whichever width, or JSON's number.
func countOf(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case uint64:
		return int(n), true
	case int:
		return n, true
	case int32:
		return int(n), true
	case uint32:
		return int(n), true
	case int16:
		return int(n), true
	case uint16:
		return int(n), true
	case int8:
		return int(n), true
	case uint8:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// mailWatch tells the client, through notify, each time more mail waits than when it last looked:
// mail received since lowers the mark, so the next arrival is told of again. With nothing waiting it
// waits on the session's mail.wait long-poll, so new mail is told of as it arrives; while mail sits
// unreceived that long-poll would answer at once, so it waits instead for the agent's next tool
// call (acted) or mcpRecheck. It returns when ctx ends.
func mailWatch(ctx context.Context, session func() (sessionContext, error), acted <-chan struct{}, notify func()) {
	told := 0
	for ctx.Err() == nil {
		n, ok := mailWaiting(ctx, session)
		if ctx.Err() != nil {
			return
		}
		if ok {
			if n > told {
				notify()
			}
			told = n
		}
		if ok && n == 0 {
			if c, err := session(); err == nil {
				if _, err := crewCall(ctx, c, "mail.wait", []any{c.Credential, int64(25000)}); err == nil {
					continue // mail arrived, or the long-poll's bound: count again
				}
			}
		}
		// NOTE: mail is waiting (mail.wait would answer at once), or the daemon can't be asked: look
		// again after the agent's next tool call, or after mcpRecheck, never in a spin.
		t := time.NewTimer(mcpRecheck)
		select {
		case <-ctx.Done():
		case <-acted:
		case <-t.C:
		}
		t.Stop()
	}
}

// callMCPTool runs tool name with its arguments, as a tools/call result: the command's answer as
// JSON text, or isError with why.
func callMCPTool(ctx context.Context, name string, args map[string]any, session func() (sessionContext, error)) map[string]any {
	failed := func(why string) map[string]any {
		return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": why}}}
	}
	var tool *mcpTool
	for i := range crewMCPTools {
		if crewMCPTools[i].Name == name {
			tool = &crewMCPTools[i]
		}
	}
	if tool == nil {
		return failed("no tool " + name)
	}
	if args == nil {
		args = map[string]any{}
	}
	command, cliArgs, err := tool.args(args)
	if err != nil {
		return failed(err.Error())
	}
	var said strings.Builder
	res, err := runCrewCommand(ctx, command, cliArgs, strings.NewReader(""), &said, session)
	var re *golibrpc.Error
	var usage *crewUsageError
	switch {
	case errors.As(err, &re):
		b, _ := json.Marshal(map[string]any{"error": &CallError{Code: re.Code, Message: re.Message}})
		return failed(string(b))
	case errors.As(err, &usage) || errors.Is(err, flag.ErrHelp):
		return failed(strings.TrimSpace(err.Error() + " " + said.String()))
	case err != nil:
		return failed(err.Error())
	}
	if name == "mail_receive" {
		res = toolAnswers(res)
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return failed(err.Error())
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}
}
