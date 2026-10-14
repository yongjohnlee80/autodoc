package script

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	lua "github.com/yuin/gopher-lua"
)

// PACKAGES — a script of several files, shared as a folder: <name>/init.lua and the modules it
// requires beside it. A package loads gated (gate.go), under its name, and its require reaches its
// own folder only: require("util") is util.lua or util/init.lua of the package, loaded once into
// the package's environment, and a module that names a path outside it (.., a root) is refused.
// A host reads the package's manifest itself and passes what it declares to Grants.Request.

// LoadPackage runs the package in fsys (its folder: init.lua at the top) as script, gated by grants.
func (e *Engine) LoadPackage(script string, fsys fs.FS, grants Grants) error {
	src, err := fs.ReadFile(fsys, "init.lua")
	if err != nil {
		return fmt.Errorf("%s: %w", script, err)
	}
	env := e.gatedEnv(script, grants)
	loaded := e.L.NewTable() // module name → its value, or true while it loads
	env.RawSetString("require", e.L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		if v := loaded.RawGetString(name); v != lua.LNil {
			if v == lua.LTrue && loading(loaded, name) {
				L.RaiseError("require %q: a loop, %q requires itself on the way", name, name)
			}
			L.Push(v)
			return 1
		}
		file, src, err := moduleOf(fsys, name)
		if err != nil {
			L.RaiseError("require %q: %s", name, err.Error())
		}
		fn, err := L.Load(bytesReader(src), script+"/"+file)
		if err != nil {
			msg := err.Error()
			var lerr *lua.ApiError
			if errors.As(err, &lerr) {
				msg = e.describe(lerr.Object)
			}
			L.RaiseError("require %q: %s", name, msg)
		}
		fn.Env = env
		loaded.RawSetString(name, lua.LTrue)
		loaded.RawSetString("\x00loading:"+name, lua.LTrue)
		L.Push(fn)
		L.Push(lua.LString(name))
		err = L.PCall(1, 1, nil)
		loaded.RawSetString("\x00loading:"+name, lua.LNil)
		if err != nil {
			loaded.RawSetString(name, lua.LNil) // a module that failed is not loaded: a later require tries again
			var lerr *lua.ApiError
			if errors.As(err, &lerr) {
				L.RaiseError("require %q: %s", name, e.describe(lerr.Object))
			}
			L.RaiseError("require %q: %s", name, err.Error())
		}
		v := L.Get(-1)
		L.Pop(1)
		if v == lua.LNil {
			v = lua.LTrue // a module that returns nothing is loaded all the same
		}
		loaded.RawSetString(name, v)
		L.Push(v)
		return 1
	}))
	fn, err := e.L.Load(bytesReader(src), script+"/init.lua")
	if err != nil {
		var lerr *lua.ApiError
		if errors.As(err, &lerr) {
			return fmt.Errorf("%s/init.lua: %s", script, e.describe(lerr.Object))
		}
		return err
	}
	fn.Env = env
	return e.run(script+"/init.lua", fn)
}

func loading(loaded *lua.LTable, name string) bool {
	return loaded.RawGetString("\x00loading:"+name) == lua.LTrue
}

// moduleOf finds module name in fsys: a.b is a/b.lua, else a/b/init.lua. Only names of the
// package's own files: letters, digits, _ and -, joined by dots.
func moduleOf(fsys fs.FS, name string) (string, []byte, error) {
	for _, part := range strings.Split(name, ".") {
		if part == "" || strings.ContainsFunc(part, func(r rune) bool {
			return !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
		}) {
			return "", nil, errors.New("a module is the package's own: names of letters, digits, _ and -, joined by dots")
		}
	}
	base := strings.ReplaceAll(name, ".", "/")
	for _, file := range []string{base + ".lua", path.Join(base, "init.lua")} {
		if src, err := fs.ReadFile(fsys, file); err == nil {
			return file, src, nil
		}
	}
	return "", nil, fmt.Errorf("no %s.lua or %s/init.lua in the package", base, base)
}
