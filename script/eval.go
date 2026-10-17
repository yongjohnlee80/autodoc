package script

import (
	"context"
	"errors"
	"strings"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

// A SHELL'S ENVIRONMENT AND EVAL — what a Lua shell runs a chunk in (ADR 1792256103 §4.3).
//
// A Shell is a table whose reads fall back to the engine's globals and whose writes stay in it, so
// x = 1 typed in a shell is that shell's, never a loaded script's. Only assignment is kept apart: a
// shell that changes a shared table (autodoc, string) changes it for every script, as a lua/*.lua
// file can, under the same trust. The engine owns every Shell: a function made in one is the
// engine's, so its callbacks run (Callback), until CloseShell. Every method is the loop's, as the
// engine is.

// Shell is one REPL's environment in an engine.
type Shell struct {
	env   *lua.LTable
	print func(string)
}

// NewShell is a Shell whose print writes to out and whose ed is the page.
func (e *Engine) NewShell(out func(string)) *Shell {
	L := e.L
	env := L.NewTable()
	mt := L.NewTable()
	mt.RawSetString("__index", L.G.Global)
	L.SetMetatable(env, mt)
	sh := &Shell{env: env, print: out}
	env.RawSetString("print", L.NewFunction(sh.luaPrint))
	env.RawSetString("ed", e.editorValue())
	e.shells[env] = sh
	return sh
}

// CloseShell forgets sh: a callback made in it is stale from now on.
func (e *Engine) CloseShell(sh *Shell) { delete(e.shells, sh.env) }

func (sh *Shell) luaPrint(L *lua.LState) int {
	parts := make([]string, L.GetTop())
	for i := range parts {
		parts[i] = L.ToStringMeta(L.Get(i + 1)).String()
	}
	if sh.print != nil {
		sh.print(strings.Join(parts, "\t"))
	}
	return 0
}

// ErrIncomplete is a chunk that stops before its end: the shell asks for more.
var ErrIncomplete = errors.New("script: the chunk is not finished")

// Eval runs chunk in sh: as an expression first (return chunk), else as statements. Its values come
// back as text, one per value; an error is the engine's, or ErrIncomplete.
func (e *Engine) Eval(sh *Shell, chunk string) ([]string, error) {
	fn, err := e.L.Load(strings.NewReader("return "+chunk), "=lua")
	if err != nil {
		if fn, err = e.L.Load(strings.NewReader(chunk), "=lua"); err != nil {
			if incomplete(chunk, err) {
				return nil, ErrIncomplete
			}
			return nil, e.failure("lua", context.Background(), err)
		}
	}
	fn.Env = sh.env
	var lines []string
	err = e.core.Batch(func() error { // the chunk's edits one undo step, ed reaching the page
		prev := e.cur
		e.cur = e.core
		defer func() { e.cur = prev }()
		var cerr error
		lines, cerr = e.callAll("lua", fn)
		return cerr
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}

// callAll is call with every result fn gives, each written as the shell shows it: the chunk and
// the writing of its values (a table's __tostring is Lua too) run protected, under one deadline, so
// a raising or endless __tostring is the chunk's error, never a panic or a hang. The stack is left
// as it was, whatever fn returned or raised.
func (e *Engine) callAll(name string, fn *lua.LFunction) ([]string, error) {
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
	top := L.GetTop()
	L.Push(fn)
	// fn and the writing of its values in one protected call: run returns the values as strings
	run := L.NewFunction(func(L *lua.LState) int {
		base := L.GetTop()
		L.Push(fn)
		L.Call(0, lua.MultRet)
		n := L.GetTop() - base
		for i := 1; i <= n; i++ {
			L.Push(lua.LString(shown(L, L.Get(base+i))))
		}
		return n // only the strings, the last n on the stack
	})
	L.Push(run)
	if err := L.PCall(0, lua.MultRet, nil); err != nil {
		L.SetTop(top)
		return nil, e.failure(name, ctx, err)
	}
	out := make([]string, L.GetTop()-top)
	for i := range out {
		out[i] = L.Get(top + 1 + i).String()
	}
	L.SetTop(top)
	return out, nil
}

// shown is one value as the shell writes it: a string quoted, a table by its __tostring or as
// literal writes one. It runs inside callAll's protected call, so a __tostring that raises is an
// error of the chunk, and one that runs on is stopped by the deadline.
func shown(L *lua.LState, v lua.LValue) string {
	switch x := v.(type) {
	case lua.LString:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(string(x)) + `"`
	case *lua.LTable:
		if L.GetMetaField(x, "__tostring") != lua.LNil {
			return L.ToStringMeta(x).String()
		}
		return literal(x)
	}
	return L.ToStringMeta(v).String()
}

// incomplete is a chunk more lines can finish: a syntax error at EOF, the chunk ending outside a
// short string. The pinned scanner gives an unfinished "abc the EOF position as well, and no line
// can finish it, so the chunk's own end decides, never the parser's message.
func incomplete(chunk string, err error) bool {
	var api *lua.ApiError
	var perr *parse.Error
	if !errors.As(err, &api) || api.Type != lua.ApiErrorSyntax || !errors.As(api.Cause, &perr) || perr.Pos.Line != parse.EOF {
		return false
	}
	return tailState(chunk) != tailShortString
}

// tailKind is where a chunk's text ends, lexically.
type tailKind int

const (
	tailCode        tailKind = iota // in code, or after a -- comment's text
	tailShortString                 // inside "…" or '…' (after a \ too): never continued
	tailLongString                  // inside [[…]] or [==[…]==]: a next line continues it
	tailLongComment                 // inside --[[…]]: a next line continues it
)

// tailState scans chunk as Lua's lexer reads strings, long brackets and comments, and says where it
// ends. It does not tokenize anything else. A short string ends at its quote, or at a newline no \
// escapes (the lexer's error, past which the chunk is code again).
func tailState(chunk string) tailKind {
	state, quote, level := tailCode, byte(0), 0
	for i := 0; i < len(chunk); i++ {
		c := chunk[i]
		switch state {
		case tailCode:
			switch {
			case c == '"' || c == '\'':
				state, quote = tailShortString, c
			case c == '-' && strings.HasPrefix(chunk[i:], "--"):
				if n, ok := longBracket(chunk, i+2); ok {
					state, level, i = tailLongComment, n, i+2+n+1
					continue
				}
				nl := strings.IndexByte(chunk[i:], '\n')
				if nl < 0 {
					return tailCode
				}
				i += nl
			case c == '[':
				if n, ok := longBracket(chunk, i); ok {
					state, level, i = tailLongString, n, i+n+1
				}
			}
		case tailShortString:
			switch c {
			case '\\':
				i++ // the escaped byte, a newline included
			case quote, '\n':
				state = tailCode
			}
		case tailLongString, tailLongComment:
			if c == ']' {
				if n, ok := closeBracket(chunk, i); ok && n == level {
					state, i = tailCode, i+n+1
				}
			}
		}
	}
	return state
}

// longBracket reads an opening long bracket at s[i:]: [ then n = then [. ok false for anything else.
func longBracket(s string, i int) (n int, ok bool) {
	if i >= len(s) || s[i] != '[' {
		return 0, false
	}
	j := i + 1
	for j < len(s) && s[j] == '=' {
		j++
	}
	if j < len(s) && s[j] == '[' {
		return j - i - 1, true
	}
	return 0, false
}

// closeBracket reads a closing long bracket at s[i:]: ] then n = then ].
func closeBracket(s string, i int) (n int, ok bool) {
	j := i + 1
	for j < len(s) && s[j] == '=' {
		j++
	}
	if j < len(s) && s[j] == ']' {
		return j - i - 1, true
	}
	return 0, false
}
