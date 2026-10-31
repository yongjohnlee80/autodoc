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

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

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

// MCPTools are the crew MCP server's tools, by name: the one table a host's setup spells its
// per-tool allow rules from (agy's mcp(autodoc/<tool>), Claude's mcp__autodoc__<tool>).
func MCPTools() []string {
	names := make([]string, 0, len(crewMCPTools))
	for _, t := range crewMCPTools {
		names = append(names, t.Name)
	}
	return names
}

// MCPServerName is the name hosts register the crew MCP server under: no slash, since agy's rule
// for a tool is mcp(<server>/<tool>).
const MCPServerName = "autodoc"

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

var (
	idProp      = map[string]any{"type": "string", "description": "the item's id, exactly as mail_receive gave it"}
	bodyProp    = map[string]any{"type": "string", "description": "the text, at most 64 KiB"}
	subjectProp = map[string]any{"type": "string", "description": "a subject line (optional)"}
)

var crewMCPTools = []mcpTool{
	{Name: "mail_receive", Description: "Lease your next mail deliveries, at most 8; call it again until more is false. " +
		"Each item's answer names the tool and arguments that answer it.",
		Schema: object(map[string]any{}),
		args:   func(map[string]any) (string, []string, error) { return "mail receive", nil, nil }},
	{Name: "mail_ack", Description: "Done with a delivery (a message or an event), by the id mail_receive gave.",
		Schema: object(map[string]any{"id": idProp}, "id"),
		args: func(in map[string]any) (string, []string, error) {
			id, err := strArg(in, "id", true)
			return "mail ack", []string{"--", id}, err
		}},
	{Name: "mail_reply", Description: "Answer a request by the id mail_receive gave; it acks the delivery too, and is safe to repeat.",
		Schema: object(map[string]any{"id": idProp, "body": bodyProp, "subject": subjectProp}, "id", "body"),
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
	{Name: "mail_send", Description: "Send mail: to a codename, name@scope, user (the person you work for) or session:<id>. " +
		"kind request asks for an answer, which wakes you when it comes.",
		Schema: object(map[string]any{
			"to":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "description": "the recipients"},
			"body":    bodyProp,
			"subject": subjectProp,
			"kind":    map[string]any{"type": "string", "enum": []string{mail.KindMessage, mail.KindRequest}, "description": "message (the default) or request"},
		}, "to", "body"),
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

// crewMCPMain runs `autodoc crew mcp`: newline-delimited JSON-RPC 2.0 on stdin and stdout, one
// request at a time, until stdin closes. A tool's failure is its result (isError, the daemon's or
// the argument's message), never the server's.
func crewMCPMain(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) int {
	// the server is one session's: the context its start names (the env's path, else its folder's),
	// read again for each call as the CLI reads it, never another the process's env names later
	envPath, cwd := os.Getenv("AUTODOC_CREW_CONTEXT"), ""
	if wd, err := os.Getwd(); err == nil {
		cwd = wd
	}
	session := func() (sessionContext, error) { return findSessionContext(envPath, cwd) }
	in := bufio.NewScanner(stdin)
	in.Buffer(make([]byte, 64<<10), 4<<20)
	out := bufio.NewWriter(stdout)
	say := func(id json.RawMessage, result any, rpcErr map[string]any) {
		m := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcErr != nil {
			m["error"] = rpcErr
		} else {
			m["result"] = result
		}
		b, _ := json.Marshal(m)
		_, _ = out.Write(append(b, '\n'))
		_ = out.Flush()
	}
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
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": MCPServerName, "version": mcpTools},
				"instructions":    "AutoDoc's crew mail. When AutoDoc's notice wakes you, call mail_receive until more is false, then answer each item with the tool its answer names.",
			}, nil)
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
			say(req.ID, callMCPTool(ctx, p.Name, p.Arguments, session), nil)
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
