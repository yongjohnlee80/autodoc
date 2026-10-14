package tui

import (
	"fmt"

	"github.com/yongjohnlee80/golib/parse/markdown"
	pyaml "github.com/yongjohnlee80/golib/parse/yaml"
	"github.com/yongjohnlee80/golib/yaml"
	lua "github.com/yuin/gopher-lua"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/script"
)

// THE PAGE AND THE COMMANDS, AS A SCRIPT SEES THEM:
//
//	autodoc.document()   the page in the editor: { workspace, path, kind, derived, frontmatter,
//	                     frontmatter_error }. path is nil for the untitled draft, workspace for a
//	                     file outside every workspace; kind is how AutoDoc reads it ("markdown",
//	                     "text", "yaml", "derived", "registered"), derived the format of a derived
//	                     document ("PDF"). frontmatter is a Markdown page's, as Lua values (nil
//	                     without one); frontmatter_error says why it could not be read.
//	autodoc.commands()   the command catalog (catalog.go), then the editor's own verbs (:w, :q, …),
//	                     source "editor" with no id (script.CommandInfo).

// bindDocument gives the engine autodoc.document, and makes autodoc.commands list the catalog.
func (h *Host) bindDocument(eng *script.Engine) {
	eng.SetFunction("document", h.luaDocument)
	if h.cat != nil {
		eng.SetCommandList(h.commandInfos)
	}
}

func (h *Host) luaDocument(L *lua.LState) int {
	t := L.CreateTable(0, 6)
	f := h.file
	k := kind.Markdown // the draft is Markdown
	if f.open {
		t.RawSetString("path", lua.LString(f.path))
		if f.ws != "" {
			t.RawSetString("workspace", lua.LString(f.ws))
		}
		k = h.resolver().Of(f.path)
		if f.derived != "" {
			t.RawSetString("derived", lua.LString(f.derived))
		}
	}
	t.RawSetString("kind", lua.LString(k.String()))
	if k == kind.Markdown && f.derived == "" {
		fm, err := frontmatterOf(h.core.Value())
		if err == nil && fm != nil {
			var v lua.LValue
			if v, err = script.ToLua(L, fm); err == nil {
				t.RawSetString("frontmatter", v)
			}
		}
		if err != nil {
			t.RawSetString("frontmatter_error", lua.LString(err.Error()))
		}
	}
	L.Push(t)
	return 1
}

// frontmatterOf is a Markdown text's frontmatter as Go values (maps with string keys, in no
// order); nil for a text without any.
func frontmatterOf(text string) (any, error) {
	doc := kind.ParseMarkdown([]byte(text))
	fm := doc.Root.FirstChild
	if fm == nil || fm.Kind != markdown.KindFrontmatter {
		return nil, nil
	}
	st, err := pyaml.Parse(fm.Literal)
	if err != nil {
		return nil, fmt.Errorf("the frontmatter is not YAML: %w", err)
	}
	if len(st.Docs) == 0 {
		return nil, nil
	}
	v, err := yaml.Evaluate(st.Docs[0], yaml.Core)
	if err != nil {
		return nil, fmt.Errorf("the frontmatter is not YAML: %w", err)
	}
	return plainYAML(v)
}

// plainYAML is v with every yaml.Map a map[string]any, which ToLua carries: a key that is not a
// string (2026:, true:) is written as YAML wrote it.
func plainYAML(v any) (any, error) {
	switch x := v.(type) {
	case yaml.Map:
		out := make(map[string]any, len(x))
		for _, it := range x {
			k, ok := it.Key.(string)
			if !ok {
				k = fmt.Sprint(it.Key)
			}
			pv, err := plainYAML(it.Value)
			if err != nil {
				return nil, err
			}
			out[k] = pv
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, it := range x {
			pv, err := plainYAML(it)
			if err != nil {
				return nil, err
			}
			out[i] = pv
		}
		return out, nil
	}
	return v, nil
}

// commandInfos is autodoc.commands()'s list: every catalog entry, under the ':' names it owns now
// (an entry whose names another took has none), then the editor's own verbs no entry owns.
func (h *Host) commandInfos() []script.CommandInfo {
	var out []script.CommandInfo
	taken := map[string]bool{}
	for _, e := range h.cat.entries() {
		info := script.CommandInfo{ID: e.ID, Args: e.Args, Help: e.Help, Title: e.Title, Source: string(e.Source)}
		if own := h.cat.owned(e.ID); len(own) > 0 {
			info.Name, info.Aliases = own[0], own[1:]
			for _, n := range own {
				taken[n] = true
			}
		}
		out = append(out, info)
	}
	if cmds := h.core.Commands(); cmds != nil {
		for _, c := range cmds.All() {
			if !taken[c.Name] {
				out = append(out, script.CommandInfo{Name: c.Name, Aliases: c.Aliases, Args: c.Args, Help: c.Help, Source: "editor"})
			}
		}
	}
	return out
}
