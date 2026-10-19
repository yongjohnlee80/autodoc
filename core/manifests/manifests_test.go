package manifests

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

const head = "name = \"p\"\nkind = \"dialog\"\nprotocol = 4\ncommand = [\"./p\"]\n"

// Each rule refuses its own case with its own reason, and a manifest that cannot run still comes
// back with its defaults, to be listed.
func TestParsePluginRefusesEachRule(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"name = \"P!\"\nkind = \"dialog\"\nprotocol = 4\ncommand = [\"x\"]\n", `name "P!": want lower-case letters, digits and -`},
		{"name = \"p\"\nkind = \"popup\"\nprotocol = 4\ncommand = [\"x\"]\n", `kind "popup": want dialog or service`},
		{"name = \"p\"\nkind = \"dialog\"\nprotocol = 999\ncommand = [\"x\"]\n", "protocol 999; this AutoDoc speaks"},
		{head + "colour = \"red\"\n", `unknown key "colour"`},
		{"name = \"p\"\nkind = \"dialog\"\nprotocol = 4\n", "no command"},
		{"name = \"p\"\nkind = \"service\"\nprotocol = 1\ncommand = [\"x\"]\n", `declares kind = "service": needs protocol = 2`},
		{"name = \"p\"\nkind = \"dialog\"\nprotocol = 2\ncommand = [\"x\"]\ncalls = [\"file.open\"]\n", "declares calls: needs protocol = 3"},
		{head + "calls = [\"file.*\"]\n", `calls "file.*": one exact command id, no wildcards`},
		{head + "calls = [\"plugin.x.y\"]\n", `calls "plugin.x.y": a plugin's command is never another plugin's to run`},
		{"name = \"p\"\nkind = \"dialog\"\nprotocol = 3\ncommand = [\"x\"]\n[table]\ntitle = \"T\"\n", "declares [table]: needs protocol = 4"},
		{head + "[table]\ntitle = \" \"\n", "[table]: no title"},
		{head + "[[commands]]\nid = \"a\"\ntitle = \"A\"\nkey = \"x\"\n[[commands]]\nid = \"b\"\ntitle = \"B\"\nkey = \"x\"\n", `command "b": key "x" is "a"'s already`},
		{"name = \"p\"\nkind = \"service\"\nprotocol = 4\ncommand = [\"x\"]\n", `start = "use": nothing would start it`},
		{head + "[dialog]\nplacements = [\"middle\"]\n", `placement "middle": want center, top`},
		{head + "[dialog]\nesc = \"quit\"\n", `esc "quit": want close or hide`},
		{"name = ", "plugin.toml: "},
	} {
		m, err := ParsePlugin([]byte(c.src))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q\n err = %v\nwant %q", c.src, err, c.want)
		}
		if m == nil {
			t.Errorf("%q: no manifest returned with its reason", c.src)
		}
	}
}

// A valid manifest gets its defaults, clamped sizes and what it declares.
func TestParsePluginDefaultsAndDeclares(t *testing.T) {
	m, err := ParsePlugin([]byte("name = \"svc\"\nkind = \"service\"\nprotocol = 4\ncommand = [\"./svc\"]\nstart = \"launch\"\n" +
		"calls = [\"todos.open\"]\n[feed]\ndocument = true\n[table]\ntitle = \"Results\"\n" +
		"[[commands]]\nid = \"run\"\ntitle = \"Run\"\nkey = \"r\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Dialog.Width != DefaultWidth || m.Dialog.Height.Rows != DefaultHeight || !slices.Equal(m.Dialog.Placements, []string{"center"}) || m.Dialog.Esc != "close" {
		t.Fatalf("defaults %+v", m.Dialog)
	}
	want := []string{
		"a service, with no surface · starts with AutoDoc",
		"reads the open note's text (feed)",
		"commands: Run (r on its SPC p card)",
		"may run AutoDoc's commands: todos.open",
		"shows a table of results (Results)",
	}
	if got := m.Declares(); !slices.Equal(got, want) {
		t.Fatalf("Declares = %q\nwant %q", got, want)
	}

	card, err := ParsePlugin([]byte(head + "[dialog]\nmodal = false\nwidth = 9999\nheight = \"50%\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !card.Card() || card.Modal() || card.Dialog.Width != MaxWidth || card.Dialog.Height.Pct != 50 {
		t.Fatalf("card %+v", card.Dialog)
	}
	if got := card.Declares(); !slices.Equal(got, []string{"a card beside the page"}) {
		t.Fatalf("a card declares %q", got)
	}
	if plain, err := ParsePlugin([]byte(head)); err != nil || len(plain.Declares()) != 0 {
		t.Fatalf("a plain dialog: %v, declares %q", err, plain.Declares())
	}
}

// A manifest that stops decoding part-way keeps what was decoded before it, and gets no defaults.
// Its one key is a list whose element is the wrong type: the decoder makes the one-element list
// before it refuses the element, whatever order it walks keys in.
func TestAPluginThatStopsDecodingKeepsWhatItDecoded(t *testing.T) {
	m, err := ParsePlugin([]byte("command = [1]\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "plugin.toml: ") {
		t.Fatalf("err = %v", err)
	}
	if len(m.Command) != 1 || m.Dialog.Width != 0 || m.Start != "" {
		t.Fatalf("decoded %+v; want its one-element command kept, and no defaults", m)
	}
}

func TestReadPluginFromAFolder(t *testing.T) {
	if _, err := ReadPlugin(fstest.MapFS{}); err == nil || !strings.HasPrefix(err.Error(), "plugin.toml: ") {
		t.Fatalf("no plugin.toml: %v", err)
	}
	m, err := ReadPlugin(fstest.MapFS{"plugin.toml": {Data: []byte(head)}})
	if err != nil || m.Name != "p" {
		t.Fatalf("%+v, %v", m, err)
	}
}

func TestReadPackage(t *testing.T) {
	pkg := func(toml string, init bool) fstest.MapFS {
		fs := fstest.MapFS{"package.toml": {Data: []byte(toml)}}
		if init {
			fs["init.lua"] = &fstest.MapFile{Data: []byte("return {}")}
		}
		return fs
	}
	ok := "name = \"usage-charts\"\nversion = \"1\"\nrequires = [\"clipboard\"]\n"
	if m, err := ReadPackage(pkg(ok, true), "usage-charts"); err != nil || !slices.Equal(m.Requires, []string{"clipboard"}) {
		t.Fatalf("%+v, %v", m, err)
	}
	if _, err := ReadPackage(pkg(ok, true), ""); err != nil {
		t.Fatalf("no folder to check: %v", err)
	}
	for _, c := range []struct {
		fs     fstest.MapFS
		folder string
		want   string
	}{
		{pkg("name = \"a b\"\n", true), "", `a name of letters, digits, - and _, not "a b"`},
		{pkg(ok, true), "other", "package.toml names usage-charts, but its folder is other"},
		{pkg(ok, false), "", "no init.lua in the package's folder"},
		{pkg("name = ", true), "", "package.toml: "},
	} {
		if _, err := ReadPackage(c.fs, c.folder); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("err = %v, want %q", err, c.want)
		}
	}

	if _, err := ReadPluginPackage(pkg(ok, true), "usage-charts"); err != nil {
		t.Fatalf("a plugin's own package: %v", err)
	}
	if _, err := ReadPluginPackage(pkg(ok, true), "autodb"); err == nil || !strings.Contains(err.Error(), `package.toml names "usage-charts", but the plugin is autodb`) {
		t.Fatalf("another plugin's name: %v", err)
	}
	if _, err := ReadPluginPackage(pkg(ok, false), "usage-charts"); err == nil || !strings.Contains(err.Error(), "no init.lua in its lua folder") {
		t.Fatalf("no init.lua: %v", err)
	}
}
