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
//	ed:find(term, opts)           the next match from the cursor: l1, c1, l2, c2, or nil;
//	                              opts: { ignore_case = true, backward = true, wrap = true }
//	ed:run(line)                  run a command line, as the prompt does
//
// and the global table's notify(message) says something where the host shows messages
// (WithNotify). A host adds functions of its own to the global table with SetFunction, and tables
// of them through Global.
//
// A host function that must wait (a call to a server) never waits on the loop: it takes a Lua
// function, does its work elsewhere, and hands the answer back on the loop with Callback, which
// runs the function as a command runs (the deadline, the editor, one undo step). ToGo and ToLua
// carry the values across, shaped as msgpack carries them.
//
// A command's edits are one undo step (the editor runs every command in one). A script runs on the
// UI loop, as everything that touches the editor does, with a deadline (WithTimeout); an error, a
// script's or the deadline's, is the command's, shown in the prompt and never a crash. Scripts get
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
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// DefaultTimeout is how long one command's script, or one file's loading, may run.
const DefaultTimeout = 2 * time.Second

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
	global  string
	notify  func(string)
	timeout time.Duration
}

// WithGlobal names the global table scripts register through ("editor" by default): AutoDoc's is
// "autodoc".
func WithGlobal(name string) Option { return func(c *config) { c.global = name } }

// WithNotify is where the global table's notify(message) goes: a status line, a toast. Unset,
// notify does nothing.
func WithNotify(fn func(string)) Option { return func(c *config) { c.notify = fn } }

// WithTimeout is how long one command's script may run before it is stopped (DefaultTimeout).
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

// New is an engine registering into core's commands.
func New(core *widget.EditorCore, opts ...Option) *Engine {
	cfg := config{global: "editor", timeout: DefaultTimeout}
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
	// the base library reads files and loads code: a script has neither
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring", "require", "module"} {
		L.SetGlobal(name, lua.LNil)
	}
	e := &Engine{L: L, core: core, notify: cfg.notify, timeout: cfg.timeout}
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
	return e
}

// Close releases the Lua state.
func (e *Engine) Close() { e.L.Close() }

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
		return fmt.Errorf("%s: %w", name, err)
	}
	e.L.Push(fn)
	if err := e.L.PCall(0, 0, nil); err != nil {
		return fmt.Errorf("%s: %w", name, err)
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

// luaCommand is the global table's command{name, aliases, args, help, run, id, title}: a verb of
// the command line, replacing any registered under its name, or the host's registrar's to place.
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
	cmd := widget.Command{
		Name:    name,
		Aliases: aliases,
		Args:    lua.LVAsString(spec.RawGetString("args")),
		Help:    lua.LVAsString(spec.RawGetString("help")),
		Run:     func(x widget.CommandContext) error { return e.runCommand(name, run, x) },
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

// runCommand calls a script's run function for one command line, with the deadline.
func (e *Engine) runCommand(name string, run *lua.LFunction, x widget.CommandContext) error {
	L := e.L
	prev := e.cur
	e.cur = cmp.Or(x.Core, e.core)
	defer func() { e.cur = prev }()
	args := L.NewTable()
	for _, a := range x.Args {
		args.Append(lua.LString(a))
	}
	return e.protect(name, run, e.editorValue(), args, lua.LString(x.Line))
}

// Callback calls fn with args later, on the UI loop: when the answer a host function went to fetch
// has come (the host's own call to a server, say). It runs as a command does: under the deadline,
// its error (the script's, or the deadline's) returned and never a crash, the editor object
// reaching the editor again and fn's edits one undo step. The editor may hold another document by
// then than the command that asked had; a callback reads where things are now. name names fn in
// the error ("search.query's callback").
func (e *Engine) Callback(name string, fn *lua.LFunction, args ...lua.LValue) error {
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
	if err := L.PCall(len(args), 0, nil); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: the script ran past %s and was stopped", name, e.timeout)
		}
		var lerr *lua.ApiError
		if errors.As(err, &lerr) {
			return fmt.Errorf("%s: %s", name, lua.LVAsString(lerr.Object))
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
