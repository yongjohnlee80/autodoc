//go:build unix

package app

import (
	"bytes"
	"encoding/json"
	"flag"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

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
	if strings.Contains(src, "/tui/") || strings.Contains(src, "appendPrompt") {
		t.Error("the plugin types into the TUI")
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
