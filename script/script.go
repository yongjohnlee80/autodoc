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
// replaces it. Positions are 1-based, as Lua's and the command line's line numbers are: line 1 is
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
// (WithNotify). A host adds functions of its own to the global table with SetFunction.
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

// SetFunction adds fn to the global table as name: a host's own function for its scripts (the
// workspace's files, say). It runs on the UI loop, like the commands that call it.
func (e *Engine) SetFunction(name string, fn lua.LGFunction) {
	e.L.SetField(e.global, name, e.L.NewFunction(fn))
}

// Load runs one script; name is how its errors name it.
func (e *Engine) Load(name string, src []byte) error {
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

// luaCommand is the global table's command{name, aliases, args, help, run}: a verb of the command
// line, replacing any registered under its name.
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
	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()
	L.SetContext(ctx)
	defer L.RemoveContext()
	prev := e.cur
	e.cur = x.Core
	defer func() { e.cur = prev }()
	args := L.NewTable()
	for _, a := range x.Args {
		args.Append(lua.LString(a))
	}
	L.Push(run)
	L.Push(e.editorValue())
	L.Push(args)
	L.Push(lua.LString(x.Line))
	if err := L.PCall(3, 0, nil); err != nil {
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
