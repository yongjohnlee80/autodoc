package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// OPENING A FILE BY ITS PATH, AND THE COMMANDS AN AGENT CAN RUN: :open (:e, :edit) is
// file.open_path's ':' name, and editor.open its socket verb, one entry point over openAbsolute;
// editor.commands and `autodoc --commands-reference` list the catalog, so an agent finds a
// command rather than guessing one.

// resolveOpenPath is p as :open and editor.open take it: absolute, ~/ under the home directory, or
// relative to the root of the workspace the editor is in, whichever workspace the asker started in.
func (h *Host) resolveOpenPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	switch {
	case p == "":
		return "", errors.New("open: takes a path, absolute or relative to the workspace in use")
	case p == "~" || strings.HasPrefix(p, "~/"):
		return filepath.Join(homeDir(), strings.TrimPrefix(p, "~")), nil
	case filepath.IsAbs(p):
		return filepath.Clean(p), nil
	}
	root := h.rootOf(h.ws)
	if root == "" {
		return "", fmt.Errorf("open: no workspace is in use to find %s in: give its absolute path", p)
	}
	return filepath.Join(root, filepath.FromSlash(p)), nil
}

// runOpenPath is file.open_path's Run. From ':' the path is the whole line after the verb, spaces
// and all, as resolveOpenPath takes it. Run any other way (a key, autodoc.run, a plugin's
// host.call), it takes one absolute path, as it always has: a relative path there is refused.
func (h *Host) runOpenPath(x Invocation) error {
	if x.Name == "" {
		if len(x.Args) != 1 || !filepath.IsAbs(x.Args[0]) {
			return errors.New("file.open_path: takes one absolute path")
		}
		h.openAbsolute(x.Args[0])
		return nil
	}
	abs, err := h.resolveOpenPath(x.Line)
	if err != nil {
		return err
	}
	h.openAbsolute(abs)
	return nil
}

// editorOpen is editor.open path: the file opened as :open opens it. It answers once the open is
// on its way, with the path resolved: the open may ask the user first (unsaved changes, an HTML
// file), so editor.document shows the file once it is open.
func (h *Host) editorOpen(_ string, a editorArgs) (any, error) {
	p, err := a.str(0, "the path")
	if err != nil {
		return nil, err
	}
	abs, err := h.resolveOpenPath(p)
	if err != nil {
		return nil, &golibrpc.Error{Code: codeEditorRefused, Message: err.Error()}
	}
	h.openAbsolute(abs)
	return map[string]any{"opening": abs}, nil
}

// editorCommands is editor.commands: every command in the catalog, as commandRows lists them.
func (h *Host) editorCommands(_ string, a editorArgs) (any, error) {
	if len(a) > 0 {
		return nil, invalid("editor.commands takes no params")
	}
	if h.cat == nil {
		return []any{}, nil
	}
	var core []widget.Command
	if h.cat.cmds != nil {
		core = h.cat.cmds.All() // the editor's registry, the entries' names among them
	}
	return commandRows(h.cat.entries(), h.cat.owned, core), nil
}

// commandRows are entries as editor.commands answers them, by id: its id, ':' name and aliases
// when ':' reaches it (editor.run takes those), the arguments, what it is, and where it comes from
// (builtin, feature, lua, plugin). names are an entry's ':' names: the ones it owns now, live (a Lua
// command takes a name over), or the ones it declares. Then the editor's own ':' commands in core
// that no entry declares (w, q, replace …), by name, from "editor", with no id.
func commandRows(entries []*Entry, names func(id string) []string, core []widget.Command) []any {
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b *Entry) int { return strings.Compare(a.ID, b.ID) })
	out := make([]any, 0, len(sorted))
	for _, e := range sorted {
		row := map[string]any{"id": e.ID, "title": e.Title, "source": string(e.Source)}
		if ns := names(e.ID); len(ns) > 0 {
			row["name"] = ns[0]
			if len(ns) > 1 {
				row["aliases"] = anys(ns[1:]) // msgpack takes []any, not []string
			}
		}
		if e.Args != "" {
			row["args"] = e.Args
		}
		if e.Help != "" {
			row["help"] = e.Help
		}
		out = append(out, row)
	}
	owned := map[string]bool{}
	for _, e := range entries {
		for _, n := range e.declared() {
			owned[n] = true
		}
	}
	own := slices.DeleteFunc(slices.Clone(core), func(c widget.Command) bool { return owned[c.Name] || takenOver[c.Name] })
	slices.SortFunc(own, func(a, b widget.Command) int { return strings.Compare(a.Name, b.Name) })
	for _, c := range own {
		row := map[string]any{"name": c.Name, "source": "editor"}
		if len(c.Aliases) > 0 {
			row["aliases"] = anys(c.Aliases)
		}
		if c.Args != "" {
			row["args"] = c.Args
		}
		if c.Help != "" {
			row["help"] = c.Help
		}
		out = append(out, row)
	}
	return out
}

// commandsReference is `autodoc --commands-reference`'s table (docs/commands.md): entries by id,
// those ':' reaches with their names, then the editor's own ':' commands (golib's, those AutoDoc
// keeps).
func commandsReference(entries []*Entry) string {
	var b strings.Builder
	b.WriteString("| Command | `:` name | Arguments | What it does |\n|---|---|---|---|\n")
	cell := func(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
	declared := map[string][]string{}
	for _, e := range entries {
		declared[e.ID] = e.declared()
	}
	for _, r := range commandRows(entries, func(id string) []string { return declared[id] }, widget.DefaultCommands()) {
		m := r.(map[string]any)
		name := ""
		if v, ok := m["name"].(string); ok {
			names := append([]string{v}, asStrings(m["aliases"])...)
			for i, n := range names {
				names[i] = "`:" + n + "`"
			}
			name = strings.Join(names, ", ")
		}
		args, _ := m["args"].(string)
		if args != "" {
			args = "`" + args + "`"
		}
		what, _ := m["help"].(string)
		if what == "" {
			what, _ = m["title"].(string)
		}
		id := "—"
		if v, _ := m["id"].(string); v != "" {
			id = "`" + v + "`"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", id, name, cell(args), cell(what))
	}
	return b.String()
}

// anys is s as []any, the shape msgpack encodes.
func anys(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// asStrings is a row's []any of strings back as strings.
func asStrings(v any) []string {
	var out []string
	for _, x := range asList(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// commandLineRefusal is editor.run's refusal of line: a verb no ':' command has is named, with
// where the names are, in place of the command line's own words.
func commandLineRefusal(line string, err error) error {
	if errors.Is(err, widget.ErrUnknownCommand) {
		verb := ""
		if f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), ":")); len(f) > 0 {
			verb = strings.TrimSuffix(f[0], "!")
		}
		return &golibrpc.Error{Code: codeEditorRefused,
			Message: fmt.Sprintf("no ':' command is named %s: editor.commands lists them, and editor.open opens a file by its path", verb)}
	}
	return &golibrpc.Error{Code: codeEditorRefused, Message: err.Error()}
}
