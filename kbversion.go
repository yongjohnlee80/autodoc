package autodoc

import (
	_ "embed"
	"regexp"
)

// kbVersionLua is lua/autodoc/kb/version.lua: the version the KB's managed files are stamped with.
// It is the one source of that stamp, the Neovim side's and the daemon's alike.
//
//go:embed lua/autodoc/kb/version.lua
var kbVersionLua []byte

// KBFrontmatter is the KB's _schema/frontmatter.yaml, the other managed file.
//
//go:embed lua/autodoc/kb/templates/_schema/frontmatter.yaml
var KBFrontmatter []byte

// kbVersionField is version.lua's autodoc = "…".
var kbVersionField = regexp.MustCompile(`(?m)^\s*autodoc\s*=\s*"([^"]+)"`)

// KBManagedVersion is the autodoc_version a KB's managed files are stamped with: version.lua's, so
// a KB the daemon scaffolds and one the Neovim side scaffolds carry the same, and a KB takes a new
// copy exactly when a release changes the files (docs/decisions/crew.md#managed-kb-files).
func KBManagedVersion() string { return managedVersionIn(kbVersionLua) }

// managedVersionIn is the autodoc = "…" of a version.lua's text.
func managedVersionIn(lua []byte) string {
	m := kbVersionField.FindSubmatch(lua)
	if m == nil {
		panic("autodoc: lua/autodoc/kb/version.lua has no autodoc = \"…\"") // embedded: a test catches it first
	}
	return string(m[1])
}
