package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
)

// PLUGINS › BROWSE THE REGISTRY… — the registry's plugins and Lua packages in the Picker (pick.go):
// each row an entry, its preview what the entry says, what its manifest declares at which commit,
// and its audits as history. Install adds a plugin (addPlugin) or installs a package
// (installPackage) from the entry's repo, and that install's card asks as it does for any URL. KB
// types are listed in the add dialog's KB type field instead (registry.go).

// registryBrowse is the feature: its picker, over the list last fetched.
type registryBrowse struct {
	h    *Host
	pick *pick[regindex.IndexEntry]
}

func newRegistryBrowse(h *Host) *registryBrowse {
	b := &registryBrowse{h: h}
	b.pick = &pick[regindex.IndexEntry]{c: core{h}, dialog: "registryPicker", name: "registry",
		list:   newRows(tuidecl.NewListModel("key", "entry"), registryRowOf),
		field:  "App.registryField",
		status: "App.registryList", noun: "plugins and packages",
		match: func(e regindex.IndexEntry) string { return e.Name + " " + e.Title + " " + e.Description },
		preview: func(e regindex.IndexEntry) func(context.Context) answerOf[pane] {
			pn := registryPane(e)
			return func(context.Context) answerOf[pane] { return answerOf[pane]{v: pn} }
		},
		choose: b.install}
	return b
}

func (*registryBrowse) Name() string { return "registry" }

func (b *registryBrowse) State() map[string]any {
	return map[string]any{
		"App.registryRows": b.pick.list.model, "App.registryField": "", "App.registryList": "",
		"App.registryPreviewTitle": "", "App.registryPreviewText": "", "App.registryPreviewAt": 0,
		"App.registryNote": "", "App.registryNoteShown": false,
	}
}

func (b *registryBrowse) Commands() map[string]Command {
	return map[string]Command{"App.browseRegistry": none(b.browse)}
}

func (b *registryBrowse) Catalog() []Entry {
	return []Entry{builtin("plugins.browse", "browse the registry's plugins and Lua packages", "App.browseRegistry()", b.browse)}
}

func (b *registryBrowse) Picks() map[string]picker { return map[string]picker{"registry": b.pick} }

// browse opens the picker over the registry's plugins and packages, the index fetched as it opens
// (at most daily); an offline list says so above it.
func (b *registryBrowse) browse() {
	h := b.h
	h.notifyOngoing("registry", "reading the registry…")
	do(h, func(ctx context.Context) registryList { return fetchRegistry(ctx, time.Now()) }, func(l registryList) {
		h.registryLoaded(l)
		var entries []regindex.IndexEntry
		for _, e := range l.ix.Entries {
			if e.Kind == regindex.KindPlugin || e.Kind == regindex.KindLua {
				entries = append(entries, e)
			}
		}
		note := l.note
		if len(entries) == 0 && note == "" {
			note = "the registry lists no plugins or packages yet"
		}
		h.notifyDone("registry", fmt.Sprintf("the registry: %d plugins and packages", len(entries)))
		h.set("App.registryNote", note)
		h.set("App.registryNoteShown", note != "")
		b.pick.setAll(entries)
		b.pick.open()
	})
}

// install is Install on an entry: the plugin added, or the package installed, from its repo.
func (b *registryBrowse) install(e regindex.IndexEntry) {
	if e.Kind == regindex.KindLua {
		if err := b.h.installPackage(e.Repo); err != nil {
			b.h.notify(err.Error())
		}
		return
	}
	if b.h.plugins != nil {
		b.h.plugins.addPlugin(e.Repo)
	}
}

// registryRowOf is an entry as the picker lists it: its kind, name and title.
func registryRowOf(_ int, e regindex.IndexEntry) rowOf {
	kind := "plugin "
	if e.Kind == regindex.KindLua {
		kind = "lua    "
	}
	return rowOf{"key": e.Kind + "/" + e.Name, "entry": kind + e.Name + " — " + e.Title}
}

// registryPane is an entry's preview: what it says, what it declares at which commit, and its
// audits, newest first, as history: none of them is a claim about the commit an install would get.
func registryPane(e regindex.IndexEntry) pane {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n%s\n\n%s %s, by %s\n%s\n", e.Title, e.Description, kindNoun(e.Kind), e.Name, e.Author, e.Repo)
	if len(e.Declares.Items) > 0 {
		at := "HEAD"
		if e.Declares.Commit != "" {
			at = regindex.Short(e.Declares.Commit)
		}
		fmt.Fprintf(&b, "\n### Declares, at %s\n\n", at)
		for _, it := range e.Declares.Items {
			fmt.Fprintf(&b, "- %s\n", it)
		}
	}
	b.WriteString("\n### Audits\n\n")
	if len(e.Audits) == 0 {
		b.WriteString("never audited\n")
	}
	for _, a := range e.Audits {
		fmt.Fprintf(&b, "- audited by %s, %s, at %s (%s)\n", a.Model, a.Date, regindex.Short(a.Commit), a.Report)
	}
	b.WriteString("\nInstall asks first, as any install does: the card names the commit it would install, and whether that commit is audited.\n")
	return pane{title: e.Title, text: b.String()}
}
