package tui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// THE EDITOR SOCKET (ADR 1792014854): the page's functions as tools for the agents this TUI starts.
// The TUI serves editor.* on a unix socket of its own, <state>/editor/<pid>.sock, mode 0600, and
// tells its agents where (AUTODOC_EDITOR) and who they are (AUTODOC_AGENT); `autodoc --editor
// <verb> [params]` calls it. Every verb's first param is the agent's codename; positions are
// 1-based, as Lua's. A verb needs a capability of the agent, agent:<codename> in the Lua grants
// (luagate.go): without it the call is refused at once and the user asked, and an Allow works for
// the next call. Every edit is one undo step; an edit given the revision it read is refused when the
// text has moved on. editor.add_command writes a Lua command into lua/ only after the user's yes.
//
//	editor.document                         {path, workspace, kind, modified, revision, cursor, selection?}  editor.read
//	editor.read [from_line, to_line]        {text, revision}                                                 editor.read
//	editor.find term [opts]                 [[l1, c1, l2, c2], …]                                            editor.read
//	editor.insert line col text [rev]       {line, col, revision}                                            editor.write
//	editor.replace l1 c1 l2 c2 text [rev]   {line, col, revision}                                            editor.write
//	editor.delete l1 c1 l2 c2 [rev]         {revision}                                                       editor.write
//	editor.run line                         {ok}                                                             commands
//	editor.add_command name source          {installed}, once the user answered                              asked each time
//
// Param 1 of every verb is the agent's token, $AUTODOC_EDITOR_TOKEN: a secret made when AutoDoc
// started the agent, which the socket knows it by. A process of the same user can read another's
// environment, so the grants keep the user's agents to what each was allowed; they are no defense
// against the user's own hostile programs (ADR 1792014854 §R4).

// The codes an editor verb refuses with, beside golib's invalid params.
const (
	codeEditorNotGranted       = golibrpc.CodeAccessDenied // the capability is not granted: the user was asked
	codeEditorStale      int64 = -32095                    // the text is not the revision the agent read
	codeEditorReadOnly   int64 = -32096                    // the page is read-only
	codeEditorRefused    int64 = -32097                    // the user said no, or the command line failed
)

// editorSocket is the socket being served: its path, how to stop it, and how long a request waits
// for the loop (nanoseconds; a test shortens it).
type editorSocket struct {
	path string
	stop context.CancelFunc
	wait atomic.Int64
}

// editorRequestWait is how long a request waits for the UI loop to reach it.
// NOTE: deadline: msgpack-rpc carries no cancellation, so an agent's own timeout never reaches the
// TUI; past this, a request the loop has not reached is refused and, when the loop gets to it, does
// nothing.
const editorRequestWait = 10 * time.Second

// startEditorSocket listens on <dir>/<pid>.sock and serves editor.* until the host stops. A
// failure is said and the TUI runs on without it.
func (h *Host) startEditorSocket(dir string) {
	if dir == "" {
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.sock", os.Getpid()))
	// the folder is the user's alone before anything listens in it (editorsocket_unix.go): the
	// socket's own chmod after Listen is a second lock, not the first
	err := privateDir(dir)
	if err == nil {
		_ = os.Remove(path) // a socket left by a crash of an earlier run with this pid
	}
	var ln net.Listener
	if err == nil {
		ln, err = net.Listen("unix", path)
	}
	if err == nil {
		err = os.Chmod(path, 0o600)
	}
	if err != nil {
		h.p.Post(func() { h.failed("the editor socket for agents", err) })
		return
	}
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	for verb, handler := range h.editorVerbs() {
		srv.Handle(verb, handler)
	}
	ctx, cancel := context.WithCancel(h.ctx)
	go func() { _ = srv.Run(ctx) }()
	h.editorSock = &editorSocket{path: path, stop: func() { cancel(); _ = os.Remove(path) }}
	h.editorSock.wait.Store(int64(editorRequestWait))
}

// editorSocketPath is where the agents reach the page; "" without the socket.
func (h *Host) editorSocketPath() string {
	if h.editorSock == nil {
		return ""
	}
	return h.editorSock.path
}

// onLoopFor runs fn on the UI loop and waits for its answer, or for ctx (the connection's, which
// ends when the agent hangs up) or the request's deadline, whichever ends first.
func onLoopFor[T any](ctx context.Context, h *Host, fn func() (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.editorSock.wait.Load()))
	defer cancel()
	type answer struct {
		v   T
		err error
	}
	ch := make(chan answer, 1)
	h.p.Post(func() {
		// a request that stopped waiting (its agent hung up, its deadline passed) does nothing: no
		// edit, no card, after the agent was told it failed
		if err := ctx.Err(); err != nil {
			ch <- answer{err: err}
			return
		}
		v, err := fn()
		ch <- answer{v, err}
	})
	select {
	case a := <-ch:
		return a.v, a.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// editorArgs are a request's params after the agent's codename.
type editorArgs []any

func invalid(format string, args ...any) error {
	return &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: fmt.Sprintf(format, args...)}
}

func (a editorArgs) str(i int, what string) (string, error) {
	if i < len(a) {
		if s, ok := a[i].(string); ok {
			return s, nil
		}
	}
	return "", invalid("param %d, %s, is a string", i+2, what)
}

// num is param i as a whole number; given false when it is absent (or nil) and optional.
func (a editorArgs) num(i int, what string, optional bool) (n int, given bool, err error) {
	if i >= len(a) || a[i] == nil {
		if optional {
			return 0, false, nil
		}
		return 0, false, invalid("param %d, %s, is a number", i+2, what)
	}
	switch v := a[i].(type) {
	case int64:
		return int(v), true, nil
	case uint64:
		return int(v), true, nil
	case int:
		return v, true, nil
	case float64:
		if v == float64(int(v)) {
			return int(v), true, nil
		}
	}
	return 0, false, invalid("param %d, %s, is a whole number", i+2, what)
}

// pos is params i and i+1 as a 1-based (line, col).
func (a editorArgs) pos(i int) (widget.TextPos, error) {
	l, _, err := a.num(i, "a line", false)
	if err != nil {
		return widget.TextPos{}, err
	}
	c, _, err := a.num(i+1, "a column", false)
	return widget.TextPos{Line: l - 1, Col: c - 1}, err
}

// editorVerbs are the socket's handlers.
func (h *Host) editorVerbs() map[string]golibrpc.Handler {
	return map[string]golibrpc.Handler{
		"editor.document":    h.editorVerb("editor.read", h.editorDocument),
		"editor.read":        h.editorVerb("editor.read", h.editorRead),
		"editor.find":        h.editorVerb("editor.read", h.editorFind),
		"editor.insert":      h.editorVerb("editor.write", h.editorInsert),
		"editor.replace":     h.editorVerb("editor.write", h.editorReplace),
		"editor.delete":      h.editorVerb("editor.write", h.editorDelete),
		"editor.run":         h.editorVerb("commands", h.editorRun),
		"editor.add_command": h.editorAddCommand,
	}
}

// agentOf is the agent a request comes from: its first param is the secret the agent was started
// with ($AUTODOC_EDITOR_TOKEN), never a name it chooses, so one agent cannot use another's grants.
// It reads the token registry, which agent launches write on the UI loop: call it on the loop only.
func (h *Host) agentOf(req *golibrpc.Request) (string, editorArgs, error) {
	tok := ""
	if len(req.Params) > 0 {
		tok, _ = req.Params[0].(string)
	}
	agent := h.agents.agentOfToken(tok)
	if agent == "" {
		return "", nil, &golibrpc.Error{Code: codeEditorNotGranted,
			Message: "param 1 is not an agent's token: only the agents AutoDoc started reach the page ($AUTODOC_EDITOR_TOKEN)"}
	}
	return agent, editorArgs(req.Params[1:]), nil
}

// editorVerb is fn as a handler: run on the loop, for an agent granted capability.
func (h *Host) editorVerb(capability string, fn func(agent string, a editorArgs) (any, error)) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		return onLoopFor(ctx, h, func() (any, error) {
			agent, a, err := h.agentOf(req) // on the loop, where agent launches write the tokens
			if err != nil {
				return nil, err
			}
			who := "agent:" + agent
			if g := (luaGrants{h}); !g.Granted(who, capability) {
				g.Request(who, []string{capability})
				return nil, &golibrpc.Error{Code: codeEditorNotGranted,
					Message: fmt.Sprintf("needs %s: not granted (the user is asked)", capability)}
			}
			return fn(agent, a)
		})
	}
}

func (h *Host) editorDocument(_ string, _ editorArgs) (any, error) {
	c := h.core
	cur := c.Cursor()
	doc := map[string]any{
		"kind": kind.Markdown.String(), "modified": c.Modified(), "revision": c.Revision(),
		"cursor": []any{cur.Line + 1, cur.Col + 1}, "read_only": c.ReadOnly(),
	}
	if h.file.open {
		doc["path"], doc["kind"] = h.file.path, h.resolver().Of(h.file.path).String()
		if h.file.ws != "" {
			doc["workspace"] = h.file.ws
		}
	}
	if r, ok := c.Selection(); ok {
		doc["selection"] = []any{r.From.Line + 1, r.From.Col + 1, r.To.Line + 1, r.To.Col + 1}
	}
	return doc, nil
}

func (h *Host) editorRead(_ string, a editorArgs) (any, error) {
	lines := h.core.Lines()
	from, given, err := a.num(0, "the first line", true)
	if err != nil {
		return nil, err
	}
	if !given {
		from = 1
	}
	to, given, err := a.num(1, "the last line", true)
	if err != nil {
		return nil, err
	}
	if !given {
		to = len(lines)
	}
	from, to = max(from, 1), min(to, len(lines))
	text := ""
	if from <= to {
		text = strings.Join(lines[from-1:to], "\n")
	}
	return map[string]any{"text": text, "revision": h.core.Revision()}, nil
}

func (h *Host) editorFind(_ string, a editorArgs) (any, error) {
	term, err := a.str(0, "the term")
	if err != nil {
		return nil, err
	}
	var o widget.FindOptions
	if len(a) > 1 {
		if m, ok := a[1].(map[string]any); ok {
			o.IgnoreCase, _ = m["ignore_case"].(bool)
			o.WholeWord, _ = m["whole_word"].(bool)
		}
	}
	out := []any{}
	for _, r := range h.core.FindAll(term, o) {
		out = append(out, []any{r.From.Line + 1, r.From.Col + 1, r.To.Line + 1, r.To.Col + 1})
	}
	return out, nil
}

// edit runs fn as one undo step, refused on a read-only page or one whose text is not at the
// revision param rev gives (absent: whatever it is).
func (h *Host) edit(a editorArgs, rev int, fn func() (widget.TextPos, error)) (any, error) {
	if want, given, err := a.num(rev, "the revision read", true); err != nil {
		return nil, err
	} else if given && uint64(want) != h.core.Revision() {
		return nil, &golibrpc.Error{Code: codeEditorStale,
			Message: fmt.Sprintf("the text is at revision %d, not %d: read it again", h.core.Revision(), want)}
	}
	if h.core.ReadOnly() {
		return nil, &golibrpc.Error{Code: codeEditorReadOnly, Message: "the page is read-only"}
	}
	var end widget.TextPos
	err := h.core.Batch(func() error {
		var err error
		end, err = fn()
		return err
	})
	if errors.Is(err, widget.ErrReadOnly) {
		return nil, &golibrpc.Error{Code: codeEditorReadOnly, Message: "the page is read-only"}
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"line": end.Line + 1, "col": end.Col + 1, "revision": h.core.Revision()}, nil
}

func (h *Host) editorInsert(_ string, a editorArgs) (any, error) {
	p, err := a.pos(0)
	if err != nil {
		return nil, err
	}
	text, err := a.str(2, "the text")
	if err != nil {
		return nil, err
	}
	return h.edit(a, 3, func() (widget.TextPos, error) { return h.core.Insert(p, text) })
}

func (h *Host) editorReplace(_ string, a editorArgs) (any, error) {
	from, err := a.pos(0)
	if err != nil {
		return nil, err
	}
	to, err := a.pos(2)
	if err != nil {
		return nil, err
	}
	text, err := a.str(4, "the text")
	if err != nil {
		return nil, err
	}
	return h.edit(a, 5, func() (widget.TextPos, error) {
		return h.core.Replace(widget.TextRange{From: from, To: to}, text)
	})
}

func (h *Host) editorDelete(_ string, a editorArgs) (any, error) {
	from, err := a.pos(0)
	if err != nil {
		return nil, err
	}
	to, err := a.pos(2)
	if err != nil {
		return nil, err
	}
	res, err := h.edit(a, 4, func() (widget.TextPos, error) {
		return from, h.core.Delete(widget.TextRange{From: from, To: to})
	})
	if m, ok := res.(map[string]any); ok {
		res = map[string]any{"revision": m["revision"]}
	}
	return res, err
}

func (h *Host) editorRun(_ string, a editorArgs) (any, error) {
	line, err := a.str(0, "the command line")
	if err != nil {
		return nil, err
	}
	if err := h.core.RunCommand(line); err != nil {
		return nil, &golibrpc.Error{Code: codeEditorRefused, Message: err.Error()}
	}
	return map[string]any{"ok": true}, nil
}

// editorAddCommand is editor.add_command name source: the source must compile; the user is asked,
// showing it; Yes writes lua/<name>.lua (never over a file there) and reloads the scripts. The call
// waits for the answer, never the loop.
func (h *Host) editorAddCommand(ctx context.Context, req *golibrpc.Request) (any, error) {
	if len(req.Params) == 0 {
		return nil, invalid("param 1 is the agent's token ($AUTODOC_EDITOR_TOKEN)")
	}
	a := editorArgs(req.Params[1:]) // the agent behind the token is looked up on the loop, below
	name, err := a.str(0, "the command file's name")
	if err != nil {
		return nil, err
	}
	src, err := a.str(1, "the Lua source")
	if err != nil {
		return nil, err
	}
	if !validPackageName(name) {
		return nil, invalid("the name is letters, digits, - and _, not %q", name)
	}
	// the card shows the whole source, and what is written is exactly what it showed
	if lines := strings.Count(src, "\n") + 1; lines > maxAgentScriptLines || len(src) > maxAgentScriptBytes {
		return nil, invalid("the source is %d lines, %d bytes: a command the user is asked about is at most %d lines and %d bytes, shown whole",
			lines, len(src), maxAgentScriptLines, maxAgentScriptBytes)
	}
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	_, cerr := L.Load(strings.NewReader(src), name+".lua")
	L.Close()
	if cerr != nil {
		return nil, invalid("the source does not compile: %s", firstLine(cerr.Error()))
	}
	answered := make(chan error, 1)
	if _, err := onLoopFor(ctx, h, func() (bool, error) {
		agent, _, err := h.agentOf(req)
		if err != nil {
			return false, err
		}
		if h.luaDir == "" {
			return false, &golibrpc.Error{Code: codeEditorRefused, Message: "no Lua folder"}
		}
		path := filepath.Join(h.luaDir, name+".lua")
		if _, err := os.Stat(path); err == nil {
			return false, &golibrpc.Error{Code: codeEditorRefused, Message: "lua/" + name + ".lua exists: it is the user's"}
		}
		h.ask(Question{
			Title: fmt.Sprintf("let the agent %s add the Lua command file %s.lua?", agent, name),
			Text:  src,
			Yes:   "&Add it", No: "&No",
			OnYes: func() {
				if ctx.Err() != nil { // the agent stopped waiting: nothing is written for no one
					h.notify("not added: the agent " + agent + " stopped waiting")
					answered <- ctx.Err()
					return
				}
				answered <- h.writeAgentScript(path, src)
			},
			OnCancel: func() {
				answered <- &golibrpc.Error{Code: codeEditorRefused, Message: "the user did not add it"}
			},
		})
		return true, nil
	}); err != nil {
		return nil, err
	}
	select {
	case err := <-answered:
		if err != nil {
			return nil, err
		}
		return map[string]any{"installed": true}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// writeAgentScript writes an agent's command file, new, then reloads the scripts. It is created at
// the Yes, exclusively: a file there by then (the user's, another card's, a link) is never
// replaced, and the agent is told so.
func (h *Host) writeAgentScript(path, src string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return &golibrpc.Error{Code: codeEditorRefused, Message: "not written: " + err.Error()}
	}
	_, err = f.WriteString(src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := h.reloadScripts(); err != nil {
		h.notify(err.Error())
	}
	return nil
}

// The largest command an agent may offer: the card shows it whole.
const (
	maxAgentScriptLines = 60
	maxAgentScriptBytes = 4096
)
