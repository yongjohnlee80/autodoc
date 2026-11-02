package autodoc

import (
	_ "embed"
	"regexp"
)

// kbVersionLua is lua/autodoc/kb/version.lua: the version the KB's managed files are stamped with,
// and their digest. It is the one source of that stamp, the Neovim side's and the daemon's alike.
//
//go:embed lua/autodoc/kb/version.lua
var kbVersionLua []byte

// KBFrontmatter is the KB's _schema/frontmatter.yaml, the other managed file (kbversion_test.go
// holds version.lua's digest to it and to KBOperations).
//
//go:embed lua/autodoc/kb/templates/_schema/frontmatter.yaml
var KBFrontmatter []byte

var (
	kbVersionField = regexp.MustCompile(`(?m)^\s*autodoc\s*=\s*"([^"]+)"`)
	kbDigestField  = regexp.MustCompile(`(?m)^\s*digest\s*=\s*"([0-9a-f]{64})"`)
)

// KBManagedVersion is the autodoc_version a KB's managed files are stamped with: version.lua's, so
// a KB the daemon scaffolds and one the Neovim side scaffolds carry the same, and a KB takes a new
// copy exactly when a release changes the files (docs/decisions/crew.md#managed-kb-files).
func KBManagedVersion() string { return fieldOf(kbVersionField) }

// kbManagedDigest is version.lua's digest of the managed files.
func kbManagedDigest() string { return fieldOf(kbDigestField) }

func fieldOf(re *regexp.Regexp) string {
	m := re.FindSubmatch(kbVersionLua)
	if m == nil {
		panic("autodoc: lua/autodoc/kb/version.lua has no " + re.String()) // embedded: a test catches it first
	}
	return string(m[1])
}
