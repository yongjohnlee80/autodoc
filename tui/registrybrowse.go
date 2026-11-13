package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// PLUGINS › BROWSE THE REGISTRY… — the registry's plugins and Lua packages in the Picker (feature/pick.go):
// each row an entry, its preview what the entry says, what its manifest declares at which commit,
// and its audits as history. Install adds a plugin (addPlugin) or installs a package
// (installPackage) from the entry's repo, and that install's card asks as it does for any URL. KB
// types are listed in the add dialog's KB type field instead (registry.go).

// registryBrowse is the feature: its picker, over the list last fetched.
type registryBrowse struct {
	h    *Host
	pick *feature.Pick[regindex.IndexEntry]
}

func newRegistryBrowse(h *Host) *registryBrowse {
	b := &registryBrowse{h: h}
	b.pick = &feature.Pick[regindex.IndexEntry]{Core: core{h}, Dialog: "registryPicker", Name: "registry",
		List:   feature.NewRows(tuidecl.NewListModel("key", "entry"), b.listRow),
		Field:  "App.registryField",
		Status: "App.registryList", Noun: "plugins and packages",
		Match: func(e regindex.IndexEntry) string { return e.Name + " " + e.Title + " " + e.Description },
		Preview: func(e regindex.IndexEntry) func(context.Context) feature.AnswerOf[feature.Pane] {
			pn := registryPane(h.trf, e)
			return func(context.Context) feature.AnswerOf[feature.Pane] { return feature.AnswerOf[feature.Pane]{Value: pn} }
		},
		Choose: b.install}
	return b
}

func (*registryBrowse) Name() string { return "registry" }

func (b *registryBrowse) State() map[string]any {
	return map[string]any{
		"App.registryRows": b.pick.List.Model(), "App.registryField": "", "App.registryList": "",
		"App.registryPreviewTitle": "", "App.registryPreviewText": "", "App.registryPreviewAt": 0,
		"App.registryNote": "", "App.registryNoteShown": false,
	}
}

func (b *registryBrowse) Commands() map[string]feature.Command {
	return map[string]feature.Command{"App.browseRegistry": feature.None(b.browse)}
}

func (b *registryBrowse) Catalog() []feature.Entry {
	return []feature.Entry{feature.Builtin("plugins.browse", "browse the registry's plugins and Lua packages", "App.browseRegistry()", b.browse)}
}

func (b *registryBrowse) Picks() map[string]feature.Picker {
	return map[string]feature.Picker{"registry": b.pick}
}

// browse opens the picker over the registry's plugins and packages, the index fetched as it opens
// (at most daily); an offline list says so above it.
func (b *registryBrowse) browse() {
	h := b.h
	h.notifyOngoing("registry", h.tr("autodoc.notice.registrybrowse.reading_the_registry"))
	src := h.seams.registry
	do(h, func(ctx context.Context) registryList { return src.fetch(ctx, time.Now()) }, func(l registryList) {
		l = l.usable()
		h.registryLoaded(l)
		var entries []regindex.IndexEntry
		for _, e := range l.ix.Entries {
			if e.Kind == regindex.KindPlugin || e.Kind == regindex.KindLua {
				entries = append(entries, e)
			}
		}
		note := l.note
		if len(entries) == 0 && note == "" {
			note = h.tr("autodoc.text.registrybrowse.the_registry_lists_no_plugins_or_packages_yet")
		}
		h.notifyDone("registry", h.trf("autodoc.notice.registrybrowse.the_registry_plugins_and_packages", len(entries)))
		h.set("App.registryNote", note)
		h.set("App.registryNoteShown", note != "")
		b.pick.SetAll(entries)
		b.pick.Open()
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

// listRow is an entry as the picker lists it: its kind, name and title.
func (b *registryBrowse) listRow(_ int, e regindex.IndexEntry) feature.RowOf {
	entry := b.h.trf("autodoc.text.registrybrowse.plugin", e.Name, e.Title)
	if e.Kind == regindex.KindLua {
		entry = b.h.trf("autodoc.text.registrybrowse.lua", e.Name, e.Title)
	}
	return feature.RowOf{"key": e.Kind + "/" + e.Name, "entry": entry}
}

// registryPane is an entry's preview: what it says, what it declares at which commit, and its
// audits, newest first, as history: none of them is a claim about the commit an install would get.
// It is in the language trf (a Host's trf) says it in.
func registryPane(trf func(string, ...any) string, e regindex.IndexEntry) feature.Pane {
	var b strings.Builder
	by := trf("autodoc.text.registrybrowse.plugin_by", e.Name, e.Author)
	switch e.Kind {
	case regindex.KindLua:
		by = trf("autodoc.text.registrybrowse.lua_package_by", e.Name, e.Author)
	case regindex.KindKBType:
		by = trf("autodoc.text.registrybrowse.kb_type_by", e.Name, e.Author)
	}
	fmt.Fprintf(&b, "## %s\n\n%s\n\n%s\n%s\n", e.Title, e.Description, by, e.Repo)
	if len(e.Declares.Items) > 0 {
		at := "HEAD"
		if e.Declares.Commit != "" {
			at = regindex.Short(e.Declares.Commit)
		}
		fmt.Fprintf(&b, "\n### %s\n\n", trf("autodoc.text.registrybrowse.declares_at", at))
		for _, it := range e.Declares.Items {
			fmt.Fprintf(&b, "- %s\n", it)
		}
	}
	fmt.Fprintf(&b, "\n### %s\n\n", trf("autodoc.text.registrybrowse.audits"))
	if len(e.Audits) == 0 {
		b.WriteString(trf("autodoc.text.registrybrowse.never_audited") + "\n")
	}
	for _, a := range e.Audits { // the report first: a URL's %2F is never taken for an argument
		fmt.Fprintf(&b, "- %s\n", trf("autodoc.text.registrybrowse.audited_by_at", a.Report, regindex.Short(a.Commit), a.Date, a.Model))
	}
	b.WriteString("\n" + trf("autodoc.text.registrybrowse.install_asks_first_as_any_install_does_the_card") + "\n")
	return feature.Pane{Title: e.Title, Text: b.String()}
}
