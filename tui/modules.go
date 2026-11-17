package tui

import (
	"embed"
	"io/fs"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/controls"
	"github.com/yongjohnlee80/golib/tui/decl/panels"
)

// THE QML THIS PROGRAM SHIPS, and the modules a document imports it through.
//
//	import autodoc 1.0               the App singleton          (declared)
//	import autodoc.theme.dark 1.0    a Theme singleton          (offered: golib's five)
//	import autodoc.dialogs 1.0       the dialogs, one per file  (offered)
//	import autodoc.views 1.0         Help, About                (offered)
//	import tui.panels 1.0            Panel                      (golib's, panels.Offer)

//go:embed qml
var qmlEmbed embed.FS

// qmlFiles is the qml directory as the root, so paths read as they do under -dev: main.qml,
// dialogs/…, views/….
var qmlFiles = func() fs.FS {
	sub, err := fs.Sub(qmlEmbed, "qml")
	if err != nil {
		panic("tui: embedded qml: " + err.Error())
	}
	return sub
}()

// layout is the screen, in QML.
var layout = func() []byte {
	b, err := fs.ReadFile(qmlFiles, "main.qml")
	if err != nil {
		panic("tui: embedded main.qml: " + err.Error())
	}
	return b
}()

// moduleVersion is what every import of this program's modules asks for.
const moduleVersion = "1.0"

// modulesFrom are the modules, with the program's QML read from files (the embedded copy, Static;
// or a directory on disk under -dev, followed) and golib's themes from themeFiles, embedded and
// Static. A Static source is wrapped last: a wrapper over it would hide the mark, and HotReload
// would read it again on every poll.
func modulesFrom(files, themeFiles fs.FS) []tuidecl.ProgramOption {
	return []tuidecl.ProgramOption{
		// everything the document can reach of this program is under App
		tuidecl.Singleton("autodoc", moduleVersion, "App"),
		// golib's five themes, as autodoc.theme.<name>: the program keeps no colours of its own
		tuidecl.Themes(themeFiles, ".", "autodoc.theme", moduleVersion),
		tuidecl.Components(files, "dialogs", "autodoc.dialogs", moduleVersion),
		tuidecl.Components(files, "views", "autodoc.views", moduleVersion),
		// tui.panels: Panel, golib's docked panel
		// (docs/decisions/plugins.md#shared-panels-and-their-geometry)
		panels.Offer(),
		tuidecl.Types(controls.Types()...),
		// AutoDoc's own text in six languages (language.go), over golib's
		tuidecl.Translations(files, "i18n", "autodoc"),
	}
}
