// Package script runs editor commands written in Lua: each script registers verbs of a golib
// editor's command line (its ':' prompt and RunCommand), and edits through the editor's core
// functions. AutoDoc loads its own scripts and the user's with it; any program with a golib Editor
// can do the same.
//
//	eng := script.New(editor.Core(), script.WithGlobal("autodoc"), script.WithNotify(say))
//	defer eng.Close()
//	errs := eng.LoadDir(os.DirFS(configDir), "lua") // every lua/*.lua, in name order
//
// A script registers a command through the global table (named by WithGlobal; "editor" by
// default). Its run function gets the editor, the command's words and the rest of its line:
//
//	editor.command{
//	  name = "upper", aliases = {"up"}, args = "[line]", help = "upper-case a line",
//	  run = function(ed, args, line)
//	    local n = tonumber(args[1]) or ed:cursor()
//	    ed:replace(n, 1, n, #ed:line(n) + 1, ed:line(n):upper())
//	  end,
//	}
//
// A command with the name of one already registered (the editor's own, an earlier script's)
// replaces it. A host with a command catalog of its own takes the commands instead (SetRegistrar):
// the table's id names one there (lua.<name> when it has none) and title is how its lists call
// it, and the host decides what the name replaces. Positions are 1-based, as Lua's and the command line's line numbers are: line 1 is
// the first, column 1 the first character (a grapheme cluster). A range runs from its start up
// to, not including, its end, as the editor's do. The editor object:
//
//	ed:value()                    the whole text
//	ed:set_value(text)            replace the whole text, as one edit
//	ed:line_count()               how many lines
//	ed:line(n)                    line n's text
//	ed:lines()                    every line, a table
//	ed:cursor()                   line, column
//	ed:set_cursor(line, col)
//	ed:insert(text)               at the cursor; returns the line and column after it
//	ed:insert_at(line, col, text) the same, at a position
//	ed:replace(l1, c1, l2, c2, text)
//	ed:delete(l1, c1, l2, c2)
//	ed:text(l1, c1, l2, c2)       a range's text
//	ed:selection()                l1, c1, l2, c2, or nil with none
//	ed:select(l1, c1, l2, c2)     select a range; ed:unselect() ends the selection
//	ed:find(term, opts)           the next match from the cursor: l1, c1, l2, c2, or nil;
//	                              opts: { ignore_case, whole_word, backward, wrap, at_cursor }
//	ed:find_all(term, opts)       every match, a list of {l1, c1, l2, c2}
//	ed:replace_all(term, text, opts)  every match replaced, as one edit; how many
//	ed:mode()                     "normal", "insert", "replace", "visual", "visual_line", "visual_block"
//	ed:read_only(), ed:modified() whether edits are refused; whether the text is unsaved
//	ed:run(line)                  run a command line, as the prompt does
//
// EditorDocs documents each, as a reference lists them. run gets a fourth argument, cmd: cmd.name
// is the verb as typed (an alias, say), and cmd.bang whether it ended in '!'. The global table's
// notify(message) says something where the host shows messages (WithNotify), and its text table
// counts a string in characters, as the editor's columns do (TextDocs): text.columns(s),
// text.sub(s, i, j), text.chars(s). A host adds functions of its own to the global table with SetFunction, and tables
// of them through Global.
//
// A host function that must wait (a call to a server) never waits on the loop: it takes a Lua
// function, does its work elsewhere, and hands the answer back on the loop with Callback, which
// runs the function as a command runs (the deadline, the editor, one undo step). ToGo and ToLua
// carry the values across, shaped as msgpack carries them. To reload its scripts a host makes a new
// engine and closes the old one (never while Running); an answer coming back for a function of
// the old one is Callback's ErrStale, and dropped.
//
// A command's edits are one undo step (the editor runs every command in one). A script runs on the
// UI loop, as everything that touches the editor does, with a deadline (WithTimeout); an error, a
// script's or the deadline's, is the command's, shown in the prompt and never a crash; it is said
// by the error's value (a table's message, or its fields), without Lua's stack trace. Scripts get
// Lua's base, table, string and math libraries: no os, io, files or modules.
//
// # Concurrency Model
//
//   - Ownership: loop-goroutine-owned, as the editor core it registers into. Load, LoadDir,
//     SetFunction and every command run on the program's UI loop.
package script

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// DefaultTimeout is how long one command's script, or one file's loading, may run.
const DefaultTimeout = 2 * time.Second

// ErrStale is Callback's answer for a function another engine made: an engine a reload replaced,
// whose host function's answer came after. The function is not run.
var ErrStale = errors.New("script: a callback of a replaced engine")

// Engine is one Lua state registering into one editor's commands.
type Engine struct {
	L       *lua.LState
	core    *widget.EditorCore
	global  *lua.LTable
	notify  func(string)
	timeout time.Duration
	// cur is the core the command running now edits; the editor object's methods reach it
	cur *widget.EditorCore
	// register takes command{…} in place of the editor's registry (SetRegistrar); loading is the
	// file being loaded, which a registration names
	register func(Registration) error
	loading  string
	// list is what commands() lists in place of the editor's registry (SetCommandList)
	list func() []CommandInfo
	// clock and location are now's and date's (WithClock)
	clock    func() time.Time
	location *time.Location
	// gated scripts (gate.go): the global table's name, the gates hosts set, every gated script's
	// environment, and base, the sandbox's globals a gated environment starts from
	globalName  string
	gates       map[string]Gate
	domainGates map[string]Gate
	envs        map[*lua.LTable]*gatedScript
	base        *lua.LTable
	// handlers are on's, by event (events.go)
	handlers map[string][]*lua.LFunction
	// calling is the gated script whose proxy called the host function running now (Caller)
	calling *gatedScript
}

// Registration is one command{…} as a host's registrar takes it: its id and title, the file that
// declared it, and the command. The command's Run takes the core it is given, or the engine's
// when it is nil: a host running it from elsewhere than the prompt (a key) passes its own.
type Registration struct {
	ID      string // the table's id; "lua.<name>" when it has none
	Title   string // the table's title; its help, or its name, when it has none
	File    string // the file whose loading declared it; "" for one declared later (a callback)
	Command widget.Command
}

// Option configures an Engine under construction.
type Option func(*config)

type config struct {
	global   string
	notify   func(string)
	timeout  time.Duration
	clock    func() time.Time
	location *time.Location
}

// WithGlobal names the global table scripts register through ("editor" by default): AutoDoc's is
// "autodoc".
func WithGlobal(name string) Option { return func(c *config) { c.global = name } }

// WithNotify is where the global table's notify(message) goes: a status line, a toast. Unset,
// notify does nothing.
func WithNotify(fn func(string)) Option { return func(c *config) { c.notify = fn } }

// WithTimeout is how long one command's script may run before it is stopped (DefaultTimeout).
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

// WithClock is where now() and date() read the time, and the zone date() writes local time in
// (time.Now and time.Local by default): a test's fixed time, say.
func WithClock(now func() time.Time, loc *time.Location) Option {
	return func(c *config) { c.clock, c.location = now, loc }
}

// New is an engine registering into core's commands.
func New(core *widget.EditorCore, opts ...Option) *Engine {
	cfg := config{global: "editor", timeout: DefaultTimeout, clock: time.Now, location: time.Local}
	for _, o := range opts {
		o(&cfg)
	}
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	for _, lib := range []struct {
		name string
		open lua.LGFunction
	}{{lua.BaseLibName, lua.OpenBase}, {lua.TabLibName, lua.OpenTable}, {lua.StringLibName, lua.OpenString}, {lua.MathLibName, lua.OpenMath}} {
		L.Push(L.NewFunction(lib.open))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}
	// the base library reads files and loads code, which a script may not; reaches other
	// functions' environments (getfenv, setfenv) and makes userdata (newproxy), which a sandbox
	// keeps to itself; and writes to standard output (_printregs; print is notify's, below)
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring", "require", "module",
		"getfenv", "setfenv", "newproxy", "_printregs"} {
		L.SetGlobal(name, lua.LNil)
	}
	e := &Engine{L: L, core: core, notify: cfg.notify, timeout: cfg.timeout, clock: cfg.clock, location: cfg.location,
		globalName: cfg.global, gates: map[string]Gate{}, domainGates: map[string]Gate{}, envs: map[*lua.LTable]*gatedScript{},
		handlers: map[string][]*lua.LFunction{}}
	e.global = L.NewTable()
	L.SetField(e.global, "command", L.NewFunction(e.luaCommand))
	L.SetField(e.global, "notify", L.NewFunction(func(L *lua.LState) int {
		if e.notify != nil {
			e.notify(L.CheckString(1))
		}
		return 0
	}))
	L.SetGlobal(cfg.global, e.global)
	e.registerEditorType()
	e.registerText()
	e.registerGlobals()
	// a string's methods are the string library itself: no script reaches it through a string
	if mt, ok := L.GetMetatable(lua.LString("")).(*lua.LTable); ok {
		mt.RawSetString("__metatable", lua.LString("the string library"))
	}
	// the sandbox as it stands now is what a gated script's environment starts from
	e.base = L.NewTable()
	L.G.Global.ForEach(func(k, v lua.LValue) {
		if k != lua.LString(cfg.global) && k != lua.LString("_G") {
			e.base.RawSet(k, v)
		}
	})
	return e
}

// Close releases the Lua state.
func (e *Engine) Close() { e.L.Close() }

// Running reports whether a script is running now: a file loading, a command, a completion or a
// callback. A host does not close the engine (a reload) from inside one.
func (e *Engine) Running() bool { return e.cur != nil || e.loading != "" }

// Global is the global table scripts register through: a host adds its own fields to it, tables
// of functions among them (AutoDoc's autodoc.search.query). Only the loop touches it.
func (e *Engine) Global() *lua.LTable { return e.global }

// SetFunction adds fn to the global table as name: a host's own function for its scripts (the
// workspace's files, say). It runs on the UI loop, like the commands that call it.
func (e *Engine) SetFunction(name string, fn lua.LGFunction) {
	e.L.SetField(e.global, name, e.L.NewFunction(fn))
}

// SetRegistrar hands every command{…} to fn in place of the editor's registry: a host's own
// catalog of commands, which registers it into the editor as it sees fit. fn's error is raised in
// the script, as a refusal of the command. nil puts the editor's registry back.
func (e *Engine) SetRegistrar(fn func(Registration) error) { e.register = fn }

// Load runs one script; name is how its errors name it.
func (e *Engine) Load(name string, src []byte) error {
	e.loading = name
	defer func() { e.loading = "" }()
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	e.L.SetContext(ctx)
	defer e.L.RemoveContext()
	fn, err := e.L.Load(bytesReader(src), name)
	if err != nil {
		return e.failure(name, ctx, err)
	}
	e.L.Push(fn)
	if err := e.L.PCall(0, 0, nil); err != nil {
		return e.failure(name, ctx, err)
	}
	return nil
}

// run runs a compiled script, fn, as Load runs one: name being loaded, under the deadline.
func (e *Engine) run(name string, fn *lua.LFunction) error {
	e.loading = name
	defer func() { e.loading = "" }()
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	e.L.SetContext(ctx)
	defer e.L.RemoveContext()
	e.L.Push(fn)
	if err := e.L.PCall(0, 0, nil); err != nil {
		return e.failure(name, ctx, err)
	}
	return nil
}

// LoadDir runs every *.lua file in dir of fsys, in name order. A file that fails is one error and
// the others still load; a dir that does not exist is no scripts.
func (e *Engine) LoadDir(fsys fs.FS, dir string) []error {
	entries, err := fs.ReadDir(fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []error{err}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var errs []error
	for _, en := range entries {
		if en.IsDir() || path.Ext(en.Name()) != ".lua" {
			continue
		}
		p := path.Join(dir, en.Name())
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := e.Load(p, b); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// luaCommand is the global table's command{name, aliases, args, help, run, complete, id, title}: a
// verb of the command line, replacing any registered under its name, or the host's registrar's to
// place.
func (e *Engine) luaCommand(L *lua.LState) int {
	spec := L.CheckTable(1)
	name := lua.LVAsString(spec.RawGetString("name"))
	run, ok := spec.RawGetString("run").(*lua.LFunction)
	if name == "" || !ok {
		L.ArgError(1, "command needs a name and a run function")
		return 0
	}
	var aliases []string
	if t, ok := spec.RawGetString("aliases").(*lua.LTable); ok {
		t.ForEach(func(_, v lua.LValue) { aliases = append(aliases, lua.LVAsString(v)) })
	}
	complete, err := e.completer(name, spec.RawGetString("complete"))
	if err != nil {
		L.ArgError(1, err.Error())
		return 0
	}
	cmd := widget.Command{
		Name:     name,
		Aliases:  aliases,
		Args:     lua.LVAsString(spec.RawGetString("args")),
		Help:     lua.LVAsString(spec.RawGetString("help")),
		Run:      func(x widget.CommandContext) error { return e.runCommand(name, run, x) },
		Complete: complete,
	}
	if e.register != nil {
		r := Registration{ID: lua.LVAsString(spec.RawGetString("id")), Title: lua.LVAsString(spec.RawGetString("title")),
			File: e.loading, Command: cmd}
		if r.ID == "" {
			r.ID = "lua." + name
		}
		if r.Title == "" {
			r.Title = cmp.Or(cmd.Help, name)
		}
		if err := e.register(r); err != nil {
			L.RaiseError("%s", err.Error())
		}
		return 0
	}
	cmds := e.core.Commands()
	if cmds == nil {
		L.RaiseError("this editor has no command line")
		return 0
	}
	if err := cmds.Set(cmd); err != nil {
		L.RaiseError("%s", err.Error())
	}
	return 0
}

// runCommand calls a script's run function for one command line, with the deadline: run(ed, args,
// line, cmd), cmd the verb as typed (an alias, say) and its bang.
func (e *Engine) runCommand(name string, run *lua.LFunction, x widget.CommandContext) error {
	L := e.L
	prev := e.cur
	e.cur = cmp.Or(x.Core, e.core)
	defer func() { e.cur = prev }()
	args := L.NewTable()
	for _, a := range x.Args {
		args.Append(lua.LString(a))
	}
	cmd := L.CreateTable(0, 2)
	L.SetField(cmd, "name", lua.LString(cmp.Or(x.Name, name)))
	L.SetField(cmd, "bang", lua.LBool(x.Bang))
	return e.protect(name, run, e.editorValueFor(run), args, lua.LString(x.Line), cmd)
}

// Callback calls fn with args later, on the UI loop: when the answer a host function went to fetch
// has come (the host's own call to a server, say). It runs as a command does: under the deadline,
// its error (the script's, or the deadline's) returned and never a crash, the editor object
// reaching the editor again and fn's edits one undo step. The editor may hold another document by
// then than the command that asked had; a callback reads where things are now. name names fn in
// the error ("search.query's callback").
func (e *Engine) Callback(name string, fn *lua.LFunction, args ...lua.LValue) error {
	// a function's environment is its engine's globals or a gated script's: no script can change it
	// (no setfenv)
	if !e.owns(fn) {
		return ErrStale
	}
	return e.core.Batch(func() error {
		prev := e.cur
		e.cur = e.core
		defer func() { e.cur = prev }()
		return e.protect(name, fn, args...)
	})
}

// protect calls fn with args under the deadline; an error, fn's or the deadline's, names name. The
// deadline of a call it runs inside (a command running another through ed:run) is restored after.
func (e *Engine) protect(name string, fn *lua.LFunction, args ...lua.LValue) error {
	_, err := e.call(name, fn, 0, args...)
	return err
}

// call is protect for a function that answers: its first nret results (nil for one it did not
// give), read before the deadline is lifted.
func (e *Engine) call(name string, fn *lua.LFunction, nret int, args ...lua.LValue) ([]lua.LValue, error) {
	L := e.L
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	outer := L.Context()
	L.SetContext(ctx)
	defer func() {
		if outer != nil {
			L.SetContext(outer)
		} else {
			L.RemoveContext()
		}
	}()
	L.Push(fn)
	for _, a := range args {
		L.Push(a)
	}
	if err := L.PCall(len(args), nret, nil); err != nil {
		return nil, e.failure(name, ctx, err)
	}
	out := make([]lua.LValue, nret)
	for i := range out {
		out[i] = L.Get(-nret + i)
	}
	L.Pop(nret)
	return out, nil
}

// completer is a command's complete field as golib's Command.Complete: a list is its candidates
// as they are; a function is called as complete(ed, word, args, cmd) on the loop, as run is
// called, and answers a list of strings. A function's error, or an answer that is not a list, is
// said through notify, and Tab then shows the command's arguments.
func (e *Engine) completer(name string, v lua.LValue) (func(widget.CompleteContext) []string, error) {
	switch x := v.(type) {
	case *lua.LNilType:
		return nil, nil
	case *lua.LTable:
		fixed := stringList(x)
		return func(widget.CompleteContext) []string { return fixed }, nil
	case *lua.LFunction:
		return func(c widget.CompleteContext) []string {
			L := e.L
			prev := e.cur
			e.cur = cmp.Or(c.Core, e.core)
			defer func() { e.cur = prev }()
			args := L.NewTable()
			for _, a := range c.Args {
				args.Append(lua.LString(a))
			}
			cmd := L.CreateTable(0, 2)
			L.SetField(cmd, "name", lua.LString(cmp.Or(c.Name, name)))
			L.SetField(cmd, "bang", lua.LBool(c.Bang))
			out, err := e.call(name+"'s complete", x, 1, e.editorValueFor(x), lua.LString(c.Word), args, cmd)
			if err == nil {
				if t, ok := out[0].(*lua.LTable); ok {
					return stringList(t)
				}
				err = fmt.Errorf("%s's complete: answered a %s, not a list of strings", name, out[0].Type())
			}
			if e.notify != nil {
				e.notify(err.Error())
			}
			return nil
		}, nil
	}
	return nil, fmt.Errorf("complete is a list of strings or a function, not a %s", v.Type())
}

// stringList is t's list part as strings: a string or a number as written, anything else left out.
func stringList(t *lua.LTable) []string {
	var out []string
	for i := 1; i <= t.Len(); i++ {
		switch v := t.RawGetInt(i).(type) {
		case lua.LString, lua.LNumber:
			out = append(out, v.String())
		}
	}
	return out
}

// failure is err, a script's failure under ctx, as its message, named name: the deadline said as
// such, and a Lua error by its value, without the stack trace a notification has no room for. The
// deadline is asked again after the value is described: describing a table runs its __tostring,
// script code under the same deadline, and a formatter that ran out the time is the deadline's.
func (e *Engine) failure(name string, ctx context.Context, err error) error {
	stopped := fmt.Errorf("%s: the script ran past %s and was stopped", name, e.timeout)
	if ctx.Err() != nil {
		return stopped
	}
	var lerr *lua.ApiError
	if errors.As(err, &lerr) {
		msg := e.describe(lerr.Object)
		if ctx.Err() != nil {
			return stopped
		}
		return fmt.Errorf("%s: %s", name, msg)
	}
	return fmt.Errorf("%s: %w", name, err)
}

// describe is an error's value as words: a string or a number as it is; a table by its __tostring,
// its message field, or else its fields; anything else by its type, so that error({…}) or
// error(nil) is never an empty message.
func (e *Engine) describe(v lua.LValue) string {
	switch x := v.(type) {
	case lua.LString:
		return string(x)
	case lua.LNumber:
		return x.String()
	case *lua.LNilType:
		return "an error with no message"
	case *lua.LTable:
		if f := e.L.GetMetaField(x, "__tostring"); f != lua.LNil {
			if err := e.L.CallByParam(lua.P{Fn: f, NRet: 1, Protect: true}, x); err == nil {
				s := e.L.Get(-1)
				e.L.Pop(1)
				if s, ok := s.(lua.LString); ok {
					return string(s)
				}
			}
		}
		if m, ok := x.RawGetString("message").(lua.LString); ok {
			return string(m)
		}
		return "an error " + literal(x)
	}
	return "an error value, a " + v.Type().String() + ": " + v.String()
}

// literal is t written as a Lua table, one level deep: its list, then its string keys in order,
// a nested table as {…}; past eight fields, ….
func literal(t *lua.LTable) string {
	var fields []string
	value := func(v lua.LValue) string {
		switch x := v.(type) {
		case lua.LString:
			return fmt.Sprintf("%q", string(x))
		case lua.LNumber, lua.LBool:
			return x.String()
		case *lua.LTable:
			return "{…}"
		}
		return v.Type().String()
	}
	n := t.Len()
	for i := 1; i <= n; i++ {
		fields = append(fields, value(t.RawGetInt(i)))
	}
	var keys []string
	t.ForEach(func(k, _ lua.LValue) {
		if s, ok := k.(lua.LString); ok {
			keys = append(keys, string(s))
		}
	})
	sort.Strings(keys)
	for _, k := range keys {
		fields = append(fields, k+" = "+value(t.RawGetString(k)))
	}
	if len(fields) > 8 {
		fields = append(fields[:8], "…")
	}
	return "{" + strings.Join(fields, ", ") + "}"
}
