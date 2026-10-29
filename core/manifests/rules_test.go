package manifests

import (
	"strings"
	"testing"
	"testing/fstest"
)

// The rest of the manifest's rules, each refusing its own case with its own reason: a service's
// dialog, a dialog's start, a card's Esc, an unknown start, the commands' ids, titles and keys, and
// a call that is not a command id.
func TestParsePluginRefusesTheRestOfItsRules(t *testing.T) {
	svc := "name = \"p\"\nkind = \"service\"\nprotocol = 4\ncommand = [\"./p\"]\n"
	for _, c := range []struct{ src, want string }{
		{svc + "start = \"launch\"\n[dialog]\nwidth = 50\n", "[dialog]: a service has no dialog"},
		{head + "start = \"launch\"\n", "start: only a service starts on its own"},
		{head + "[dialog]\nmodal = false\nesc = \"close\"\n", "[dialog] esc: a card's Esc closes its panel"},
		{svc + "start = \"boot\"\n", `start "boot": want use or launch`},
		{head + "[[commands]]\nid = \"Run!\"\ntitle = \"Run\"\n", `command id "Run!": want lower-case letters, digits and -`},
		{head + "[[commands]]\nid = \"run\"\ntitle = \"Run\"\n[[commands]]\nid = \"run\"\ntitle = \"Again\"\n", `command id "run": twice`},
		{head + "[[commands]]\nid = \"run\"\n", `command "run": no title`},
		{head + "[[commands]]\nid = \"run\"\ntitle = \"Run\"\nkey = \"rr\"\n", `command "run": key "rr": want one letter or digit`},
		{head + "calls = [\"open\"]\n", `calls "open": not a command id (file.open, todos.open)`},
		{head + "calls = [\"file.open\", \"file.open\"]\n", `calls "file.open": twice`},
		{head + "[dialog]\nheight = \"5%\"\n", `height 5%: want rows, or a percentage from "10%" to "100%"`},
		{head + "[dialog]\nheight = \"80\"\n", `height 80: want rows`},
		{head + "[dialog]\nheight = 1.5\n", `height 1.5: want rows`},
	} {
		if _, err := ParsePlugin([]byte(c.src)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q:\n  got %v\n  want %q", c.src, err, c.want)
		}
	}
}

// A dialog's height in rows is rows; a service started on first use says so.
func TestAHeightInRowsAndAServiceStartedOnUse(t *testing.T) {
	m, err := ParsePlugin([]byte(head + "[dialog]\nheight = 12\n"))
	if err != nil || m.Dialog.Height.Rows != 12 || m.Dialog.Height.Pct != 0 {
		t.Fatalf("height = 12: %+v, %v", m.Dialog.Height, err)
	}
	s, err := ParsePlugin([]byte("name = \"p\"\nkind = \"service\"\nprotocol = 4\ncommand = [\"./p\"]\nstart = \"use\"\n" +
		"[[commands]]\nid = \"go\"\ntitle = \"Go\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Declares(); len(got) == 0 || got[0] != "a service, with no surface · starts on first use" {
		t.Errorf("a service started on use declares %q", got)
	}
}

// A plugin's Lua package without a package.toml, or with one that does not decode, is refused
// naming the file.
func TestReadPluginPackageRefusesAMissingOrBrokenFile(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"missing": {},
		"broken":  {"package.toml": {Data: []byte("name = ")}},
	} {
		if _, err := ReadPluginPackage(fsys, "p"); err == nil || !strings.HasPrefix(err.Error(), "package.toml: ") {
			t.Errorf("%s: %v, want a package.toml refusal", name, err)
		}
	}
}
