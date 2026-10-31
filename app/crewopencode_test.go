//go:build unix

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestThePluginWakesWithTheFixedNoticeAlone: the plugin's notice is crew.WakeNotice, the one it
// wakes with; it never reads text from a request (a stray connection of the user's can do no more
// than a spurious wake), bounds the request, acts on a whole line alone, refuses a session not its
// own, keeps its socket in a folder only the user can reach, and never types into the TUI.
func TestThePluginWakesWithTheFixedNoticeAlone(t *testing.T) {
	t.Parallel()
	src := string(opencodePlugin)
	notice, _ := json.Marshal(crew.WakeNotice)
	for what, want := range map[string]string{
		"the notice is crew.WakeNotice":          "const NOTICE = " + string(notice) + ";",
		"it wakes with that notice":              `parts: [{ type: "text", text: NOTICE }]`,
		"the request is bounded":                 "if (s.data.buf.length > MAX_REQUEST) {",
		"only a whole line is acted on":          `if (nl < 0) return;`,
		"another session is refused":             `if (!session || req?.session !== session) return reply(s, { ok: false, refused:`,
		"its folder is made 0700":                "mkdirSync(dir, { mode: 0o700 })",
		"a folder others can reach is refused":   "(st.mode & 0o077) !== 0",
		"inert outside a crew session":           "if (!process.env.AUTODOC_CREW_CONTEXT) return {};",
		"inert under the session's own opencode": "if (holder && holder !== process.pid && alive(holder)) return {};",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("%s: the plugin lacks %q", what, want)
		}
	}
	if n := strings.Count(src, "text: "); n != 1 {
		t.Errorf("the plugin sends text in %d places, want the notice's alone", n)
	}
	if m := regexp.MustCompile(`req(\?)?\.(notice|text|message|content|parts)`).FindString(src); m != "" {
		t.Errorf("the plugin reads %s from a request", m)
	}
	// the TUI is asked to show a session, never typed into: select-session is its one route
	for _, typing := range []string{"appendPrompt", "submitPrompt", "append-prompt", "submit-prompt"} {
		if strings.Contains(src, typing) {
			t.Errorf("the plugin types into the TUI (%s)", typing)
		}
	}
	if routes := regexp.MustCompile(`/tui/[a-z/-]+`).FindAllString(src, -1); len(slices.DeleteFunc(routes, func(r string) bool { return r == "/tui/select-session" })) > 0 {
		t.Errorf("the plugin calls the TUI's %v, beyond select-session", routes)
	}
}

// TestThePluginIsWrittenWhole: the plugin is written into its folder as embedded, readable by
// opencode, and an unchanged one is left as it is (not written again); one that differs (an older
// AutoDoc's) is replaced.
func TestThePluginIsWrittenWhole(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "opencode")
	path, err := writeOpenCodePlugin(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Equal(b, opencodePlugin) {
		t.Fatalf("the written plugin: %v, equal %v", err, bytes.Equal(b, opencodePlugin))
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o644 || filepath.Base(path) != opencodePluginName {
		t.Errorf("the plugin is %s %v", filepath.Base(path), fi.Mode().Perm())
	}
	before, _ := os.Stat(path)
	if _, err := writeOpenCodePlugin(dir); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); !os.SameFile(before, after) {
		t.Error("an unchanged plugin was written again")
	}
	if err := os.WriteFile(path, []byte("// an older plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeOpenCodePlugin(dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, opencodePlugin) {
		t.Error("an older plugin was not replaced")
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("the folder holds %d files, want the plugin alone (no temporary left)", len(es))
	}
}

// TestTheOpenCodeConfigExtendsWhatIsSet: OPENCODE_CONFIG_CONTENT gets the plugin in its plugin
// list; a value already set is kept whole, its list extended (never replaced, nor the plugin
// listed twice), and one that is no JSON object, or whose plugin is no list, is refused.
func TestTheOpenCodeConfigExtendsWhatIsSet(t *testing.T) {
	t.Parallel()
	const p = "/d/autodoc-crew.js"
	for in, want := range map[string]string{
		"": `{"plugin":["file:///d/autodoc-crew.js"]}`,
		`{"theme":"x","plugin":["file:///u.js"]}`:  `{"plugin":["file:///u.js","file:///d/autodoc-crew.js"],"theme":"x"}`,
		`{"plugin":["file:///d/autodoc-crew.js"]}`: `{"plugin":["file:///d/autodoc-crew.js"]}`,
		`{"n":12345678901234567890,"plugin":[]}`:   `{"n":12345678901234567890,"plugin":["file:///d/autodoc-crew.js"]}`,
		`{"agent":{"a":{"prompt":"<&>"}}}`:         `{"agent":{"a":{"prompt":"<&>"}},"plugin":["file:///d/autodoc-crew.js"]}`,
	} {
		if got, err := opencodeConfigContent(in, p); err != nil || got != want {
			t.Errorf("%q: %s %v, want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"null", "[]", "{", `{"plugin":"file:///u.js"}`} {
		if got, err := opencodeConfigContent(bad, p); err == nil {
			t.Errorf("%q: %s, want refused", bad, got)
		}
	}
}

// TestCrewPluginPrintsWhatLoadsIt: `autodoc crew plugin --opencode` writes the plugin and prints
// its path; with --print-env, the environment line that loads it, merged with the one set.
func TestCrewPluginPrintsWhatLoadsIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	if code := crewPluginMain([]string{"--opencode", "--dir", dir}, func(string) string { return "" }, &out, &errOut); code != 0 ||
		strings.TrimSpace(out.String()) != filepath.Join(dir, opencodePluginName) {
		t.Fatalf("exit %d: %q %s", code, out.String(), errOut.String())
	}
	out.Reset()
	env := func(k string) string {
		if k == "OPENCODE_CONFIG_CONTENT" {
			return `{"plugin":["file:///u.js"]}`
		}
		return ""
	}
	if code := crewPluginMain([]string{"--opencode", "--print-env", "--dir", dir}, env, &out, &errOut); code != 0 ||
		strings.TrimSpace(out.String()) != `OPENCODE_CONFIG_CONTENT={"plugin":["file:///u.js","file://`+filepath.Join(dir, opencodePluginName)+`"]}` {
		t.Errorf("--print-env: exit %d: %q %s", code, out.String(), errOut.String())
	}
	if code := crewPluginMain([]string{"--dir", dir}, env, &out, &errOut); code != 2 {
		t.Errorf("no --opencode: exit %d, want 2", code)
	}
}

// TestAnOpenCodeLocatorIsItsParentsPluginSocket: the locator names the plugin socket of the
// process that started the command (here the test process, holding a stand-in), that process's
// reference and the session; with no socket there, a file that is no socket there, or no session,
// there is none.
func TestAnOpenCodeLocatorIsItsParentsPluginSocket(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	pid := os.Getpid()
	if _, err := opencodeLocator("ses_1", pid, []string{dir}); err == nil {
		t.Error("a locator with no socket")
	}
	sock := filepath.Join(dir, strconv.Itoa(pid)+".sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := opencodeLocator("ses_1", pid, []string{dir}); err == nil {
		t.Error("a locator for a file that is no socket")
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	loc, err := opencodeLocator("ses_1", pid, []string{filepath.Join(dir, "none"), dir})
	if err != nil {
		t.Fatal(err)
	}
	var l struct {
		Socket, Start, Session string
		PID                    int
	}
	if err := json.Unmarshal([]byte(loc), &l); err != nil {
		t.Fatal(err)
	}
	self, _ := crew.ProcRef(pid)
	if l.Socket != sock || l.PID != pid || l.Start != self || l.Session != "ses_1" {
		t.Errorf("the locator %s", loc)
	}
	if _, err := opencodeLocator("", pid, []string{dir}); err == nil {
		t.Error("a locator with no session")
	}
}

// TestCrewEndpointOpenCodeNeedsItsOwnContext: `crew endpoint --opencode` is the plugin's, from its
// session's own opencode: with no $AUTODOC_CREW_CONTEXT (a context found by folder alone) it is
// refused before anything is called.
func TestCrewEndpointOpenCodeNeedsItsOwnContext(t *testing.T) {
	t.Setenv("AUTODOC_CREW_CONTEXT", "")
	fs := flag.NewFlagSet("crew endpoint", flag.ContinueOnError)
	crewCommands["crew endpoint"].flags(fs)
	if err := fs.Parse([]string{"--opencode", "ses_1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := crewCommands["crew endpoint"].call(sessionContext{Credential: "c"}, fs, fs.Args(), nil); err == nil ||
		!strings.Contains(err.Error(), "AUTODOC_CREW_CONTEXT") {
		t.Errorf("--opencode with no $AUTODOC_CREW_CONTEXT: %v, want refused", err)
	}
}

// bunFor is the Bun the plugin's executable controls run on ($AUTODOC_TEST_BUN, else bun on PATH):
// the runtime opencode embeds. With none, the cell is skipped, unless $AUTODOC_TEST_BUN_REQUIRED
// says the gate has one, when its absence fails loudly.
func bunFor(t *testing.T) string {
	t.Helper()
	if b := os.Getenv("AUTODOC_TEST_BUN"); b != "" {
		return b
	}
	if b, err := exec.LookPath("bun"); err == nil {
		return b
	}
	if os.Getenv("AUTODOC_TEST_BUN_REQUIRED") != "" {
		t.Fatal("AUTODOC_TEST_BUN_REQUIRED is set and no bun was found ($AUTODOC_TEST_BUN, PATH)")
	}
	t.Skip("no bun ($AUTODOC_TEST_BUN, PATH): the plugin's executable controls need the runtime opencode embeds")
	return ""
}

// TestThePluginsBunControls runs the plugin's executable controls (opencode/autodoc-crew.test.js)
// on Bun: its registration (the latest session wins in either completion order, one run at a time;
// a stalled check or CLI cut at its bound, its child killed, and retried; a failed spawn contained;
// dispose cancelling and draining one in flight), and its socket (the fixed notice alone, the
// refusals, the modes).
func TestThePluginsBunControls(t *testing.T) {
	bun := bunFor(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "test", "./opencode/autodoc-crew.test.js")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bun test: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// TestThePluginURLNamesTheFile: the plugin's URL is absolute and escaped, so it names the very
// file wherever it is: a folder holding '#', '%', '?', a space or non-ASCII, and a relative --dir
// (made absolute against the working directory) all read back as their own path.
func TestThePluginURLNamesTheFile(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"team#1", "50%41", "a?b=c", "with space", "ünïcödé", "%", "plain"} {
		p := filepath.Join(base, name, opencodePluginName)
		u, err := pluginURL(p)
		if err != nil {
			t.Fatal(err)
		}
		back, err := url.Parse(u)
		if err != nil || back.Scheme != "file" || back.Host != "" || back.Path != p || back.RawQuery != "" || back.Fragment != "" {
			t.Errorf("%q: %s reads back as %+v %v, want the path %s", name, u, back, err, p)
		}
	}
	t.Chdir(base)
	if u, err := pluginURL(filepath.Join("rel", opencodePluginName)); err != nil || u != (&url.URL{Scheme: "file", Path: filepath.Join(base, "rel", opencodePluginName)}).String() {
		t.Errorf("a relative path's URL: %s %v, want it absolute", u, err)
	}
	var out, errOut bytes.Buffer
	if code := crewPluginMain([]string{"--opencode", "--dir", "rel dir"}, func(string) string { return "" }, &out, &errOut); code != 0 ||
		strings.TrimSpace(out.String()) != filepath.Join(base, "rel dir", opencodePluginName) {
		t.Errorf("a relative --dir prints %q (exit %d), want the absolute path", out.String(), code)
	}
	out.Reset()
	if code := crewPluginMain([]string{"--opencode", "--print-env", "--dir", "rel dir"}, func(string) string { return "" }, &out, &errOut); code != 0 {
		t.Fatalf("a relative --dir: exit %d %s", code, errOut.String())
	}
	want := (&url.URL{Scheme: "file", Path: filepath.Join(base, "rel dir", opencodePluginName)}).String()
	if got := strings.TrimSpace(out.String()); got != `OPENCODE_CONFIG_CONTENT={"plugin":["`+want+`"]}` {
		t.Errorf("a relative --dir: %s, want the absolute %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(base, "rel dir", opencodePluginName)); err != nil {
		t.Errorf("a relative --dir: the plugin is not where its URL says: %v", err)
	}
}

// TestThePluginLoadsFromItsURL: Bun, opencode's runtime, imports the plugin through the URL
// `crew plugin --print-env` gives, for a folder whose name a bare "file://"+path would misname.
func TestThePluginLoadsFromItsURL(t *testing.T) {
	bun := bunFor(t)
	for _, name := range []string{"team#1 50%41 ünï", "plain"} {
		dir := filepath.Join(t.TempDir(), name)
		var out, errOut bytes.Buffer
		if code := crewPluginMain([]string{"--opencode", "--print-env", "--dir", dir}, func(string) string { return "" }, &out, &errOut); code != 0 {
			t.Fatalf("%q: exit %d %s", name, code, errOut.String())
		}
		var cfg struct{ Plugin []string }
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(out.String()), "OPENCODE_CONFIG_CONTENT=")), &cfg); err != nil || len(cfg.Plugin) != 1 {
			t.Fatalf("%q: %s %v", name, out.String(), err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		cmd := exec.CommandContext(ctx, bun, "-e", `const m = await import(process.env.AUTODOC_PLUGIN_URL); if (typeof m.AutodocCrew !== "function") { console.error("no AutodocCrew"); process.exit(3) }`)
		cmd.Env = append(os.Environ(), "AUTODOC_PLUGIN_URL="+cfg.Plugin[0])
		b, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Errorf("%q: bun could not load %s: %v\n%s", name, cfg.Plugin[0], err, b)
		}
	}
}

// TestThePluginsEnvRefusesAFolderOpenCodeCantLoadFrom: opencode loads no plugin from a path holding
// a '?', escaped or not, so --print-env refuses such a folder, saying so, rather than print an
// environment that loads nothing.
func TestThePluginsEnvRefusesAFolderOpenCodeCantLoadFrom(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	dir := filepath.Join(t.TempDir(), "a?b")
	if code := crewPluginMain([]string{"--opencode", "--print-env", "--dir", dir}, func(string) string { return "" }, &out, &errOut); code != 1 ||
		out.Len() != 0 || !strings.Contains(errOut.String(), "holds a '?'") {
		t.Errorf("a folder holding '?': exit %d, %q %q, want refused", code, out.String(), errOut.String())
	}
}
