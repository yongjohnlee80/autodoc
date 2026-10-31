//go:build unix

package app

import (
	"bytes"
	"encoding/json"
	"flag"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestCrewPluginPrintsWhatLoadsIt: `autodoc crew plugin --opencode` writes the plugin and prints
// its path; with --print-env, the environment line that loads it, merged with the one set.
func TestCrewPluginPrintsWhatLoadsIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	if code := crewPluginMain("", []string{"--opencode", "--dir", dir}, func(string) string { return "" }, &out, &errOut); code != 0 ||
		strings.TrimSpace(out.String()) != filepath.Join(dir, crew.OpenCodePluginName) {
		t.Fatalf("exit %d: %q %s", code, out.String(), errOut.String())
	}
	out.Reset()
	env := func(k string) string {
		if k == "OPENCODE_CONFIG_CONTENT" {
			return `{"plugin":["file:///u.js"]}`
		}
		return ""
	}
	if code := crewPluginMain("", []string{"--opencode", "--print-env", "--dir", dir}, env, &out, &errOut); code != 0 ||
		strings.TrimSpace(out.String()) != `OPENCODE_CONFIG_CONTENT={"plugin":["file:///u.js","file://`+filepath.Join(dir, crew.OpenCodePluginName)+`"]}` {
		t.Errorf("--print-env: exit %d: %q %s", code, out.String(), errOut.String())
	}
	if code := crewPluginMain("", []string{"--dir", dir}, env, &out, &errOut); code != 2 {
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

// TestCrewPluginMakesARelativeDirAbsolute: a relative --dir is made absolute against the working
// directory, in the path printed, in the URL --print-env gives, and where the plugin is written.
func TestCrewPluginMakesARelativeDirAbsolute(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	var out, errOut bytes.Buffer
	if code := crewPluginMain("", []string{"--opencode", "--dir", "rel dir"}, func(string) string { return "" }, &out, &errOut); code != 0 ||
		strings.TrimSpace(out.String()) != filepath.Join(base, "rel dir", crew.OpenCodePluginName) {
		t.Errorf("a relative --dir prints %q (exit %d), want the absolute path", out.String(), code)
	}
	out.Reset()
	if code := crewPluginMain("", []string{"--opencode", "--print-env", "--dir", "rel dir"}, func(string) string { return "" }, &out, &errOut); code != 0 {
		t.Fatalf("a relative --dir: exit %d %s", code, errOut.String())
	}
	want := (&url.URL{Scheme: "file", Path: filepath.Join(base, "rel dir", crew.OpenCodePluginName)}).String()
	if got := strings.TrimSpace(out.String()); got != `OPENCODE_CONFIG_CONTENT={"plugin":["`+want+`"]}` {
		t.Errorf("a relative --dir: %s, want the absolute %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(base, "rel dir", crew.OpenCodePluginName)); err != nil {
		t.Errorf("a relative --dir: the plugin is not where its URL says: %v", err)
	}
}

// TestThePluginsEnvRefusesAFolderOpenCodeCantLoadFrom: opencode loads no plugin from a path holding
// a '?', escaped or not, so --print-env refuses such a folder, saying so, rather than print an
// environment that loads nothing.
func TestThePluginsEnvRefusesAFolderOpenCodeCantLoadFrom(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	dir := filepath.Join(t.TempDir(), "a?b")
	if code := crewPluginMain("", []string{"--opencode", "--print-env", "--dir", dir}, func(string) string { return "" }, &out, &errOut); code != 1 ||
		out.Len() != 0 || !strings.Contains(errOut.String(), "holds a '?'") {
		t.Errorf("a folder holding '?': exit %d, %q %q, want refused", code, out.String(), errOut.String())
	}
}
