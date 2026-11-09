package tui

import (
	"fmt"
	"os"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/keymap"
)

// KEY-MAPPING FILES — a user's keymap.xml beside config.toml moves any key by its command's id
// (docs/keymap.md). CheckKeymap is `autodoc --check-keymap`: the file read as the program reads it,
// with no UI or daemon.

// CheckKeymap reads file as the program would read it: every problem that keeps a key from working
// is its error (golib's tuidecl.CheckKeymap: an id nobody declares, two commands of one scope on one
// key, a malformed file, each with its line), and warnings are the keys it moves onto one an inner
// widget takes first. A missing file is an error here, though the program reads it as none.
func CheckKeymap(file string) (warnings []keymap.Problem, err error) {
	if _, err := os.Stat(file); err != nil {
		return nil, err
	}
	h := newHost(NewSession("", nil), Options{})
	return tuidecl.CheckKeymap(h.options(Options{KeymapFile: file})...)
}

// The generated block of docs/keymap.md: every AutoDoc command and its keys.
const (
	keymapDocBegin = "<!-- the commands: generated from tui/keys.go (go test ./tui -run TestKeysAreGenerated -update) -->\n"
	keymapDocEnd   = "<!-- end of the generated commands -->\n"
)

// keymapTable is the commands, a row each: its id, its scope and its default keys, in the catalog's
// order.
func keymapTable(cmds []keymap.Command) string {
	var b strings.Builder
	b.WriteString("| id | scope | keys |\n| --- | --- | --- |\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", c.ID, c.Scope, strings.ReplaceAll(seqNames(c.Keys), "|", "\\|"))
	}
	return b.String()
}

// KeyCommandsForReference are AutoDoc's own key commands, as the reference lists them.
func KeyCommandsForReference() []keymap.Command {
	cmds, err := keyCatalog(newHost(NewSession("", nil), Options{}).features()).commands()
	if err != nil {
		panic(err) // the registry's check refuses it first: a build that cannot start
	}
	return cmds
}
