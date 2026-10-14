package script

import (
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// CommandInfo is one command as the global table's commands() lists it. ID, Title and Source are
// a host catalog's (SetCommandList); without one they are empty, and commands() lists the
// editor's registry.
type CommandInfo struct {
	ID      string   // the host's id for it: "lua.upper", "file.save"
	Name    string   // its ':' name; "" for one ':' does not reach
	Aliases []string // its other ':' names
	Args    string   // what Tab shows after the name
	Help    string   // one line
	Title   string   // what the host's lists call it
	Source  string   // who declared it: "lua", "plugin", "editor", …
}

// SetCommandList makes fn what commands() lists: a host's catalog of commands, in place of the
// editor's registry. nil puts the registry back.
func (e *Engine) SetCommandList(fn func() []CommandInfo) { e.list = fn }

// GlobalDocs is the global table's own functions as the reference lists them, beside command and
// notify, the table named global: now, date, commands and on.
func GlobalDocs(global string) []Doc {
	var e Engine
	fns := append(e.timeFunctions(), e.commandsFunction(), e.onFunction())
	out := make([]Doc, len(fns))
	for i, f := range fns {
		out[i] = Doc{Call: global + "." + f.name + "(" + f.args + ")", Returns: f.returns, Notes: f.notes}
	}
	return out
}

func (e *Engine) onFunction() function {
	return function{"on", "event, fn", "nothing", "Calls `fn(ed, info)` each time event happens, from now until the scripts reload. The host says which events there are ([events](#events)). A handler runs as a callback does, and its error is a notification.", e.luaOn}
}

func (e *Engine) commandsFunction() function {
	return function{"commands", "", "list of tables", "Every command, in the order they were added: `{id, name, aliases, args, help, title, source}`. `name` is its `:` name, `\"\"` for one only a key or its id runs; `source` says whose it is (`\"lua\"`, `\"plugin\"`, `\"editor\"`, …). A command that takes another's `:` name finds that one here, under its id and with no `name`, and runs it with `autodoc.run`.", e.luaCommands}
}

// registerGlobals adds now, date and commands to the global table, and makes print say what it
// prints where notify does.
func (e *Engine) registerGlobals() {
	for _, f := range append(e.timeFunctions(), e.commandsFunction(), e.onFunction()) {
		e.L.SetField(e.global, f.name, e.L.NewFunction(f.fn))
	}
	e.L.SetGlobal("print", e.L.NewFunction(e.luaPrint))
}

// luaPrint is print: its arguments as tostring writes them, a tab between, said by notify; a
// script has no standard output to write to.
func (e *Engine) luaPrint(L *lua.LState) int {
	parts := make([]string, L.GetTop())
	for i := range parts {
		parts[i] = L.ToStringMeta(L.Get(i + 1)).String()
	}
	if e.notify != nil {
		e.notify(strings.Join(parts, "\t"))
	}
	return 0
}

func (e *Engine) luaCommands(L *lua.LState) int {
	var all []CommandInfo
	if e.list != nil {
		all = e.list()
	} else if cmds := e.core.Commands(); cmds != nil {
		for _, c := range cmds.All() {
			all = append(all, CommandInfo{Name: c.Name, Aliases: c.Aliases, Args: c.Args, Help: c.Help})
		}
	}
	t := L.CreateTable(len(all), 0)
	for _, c := range all {
		row := L.CreateTable(0, 7)
		aliases := L.CreateTable(len(c.Aliases), 0)
		for _, a := range c.Aliases {
			aliases.Append(lua.LString(a))
		}
		for k, v := range map[string]lua.LValue{"id": lua.LString(c.ID), "name": lua.LString(c.Name), "aliases": aliases,
			"args": lua.LString(c.Args), "help": lua.LString(c.Help), "title": lua.LString(c.Title), "source": lua.LString(c.Source)} {
			row.RawSetString(k, v)
		}
		t.Append(row)
	}
	L.Push(t)
	return 1
}
