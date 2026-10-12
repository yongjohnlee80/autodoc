package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A failure says what failed, then why (failed): the browser that does not open a preview or a
// page's link; a recovery copy that cannot be removed; a theme
// whose document cannot be read; a plugin that does not start, from its command, from the
// launch of the services and from the feed.
func TestAFailureSaysWhatFailedAndWhy(t *testing.T) {
	r := runTUI(t, NewSession("unused", nil), Options{})
	noBrowser := errors.New("no browser here")
	onLoop(r, func() bool {
		r.h.browser = func(context.Context, string) error { return noBrowser }
		r.h.htmlPreviewPath = "/tmp/preview.html"
		r.h.openPreviewInBrowser()
		r.h.htmlLink("https://example.com/page")
		return true
	})
	r.waitKept(t, "the browser did not open: no browser here")
	r.waitKept(t, "open https://example.com/page: no browser here")

	// a copy that is a folder with something in it cannot be removed
	dir := t.TempDir()
	copyPath := recoveryFile(dir, "kb", "a.md")
	if err := os.MkdirAll(filepath.Join(copyPath, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.recoveryDir = dir; r.h.dropRecovery("kb", "a.md"); return true })
	r.waitKept(t, "could not remove the recovery copy of a.md: ")

	// a theme switch reads the document from the dev folder, which has none
	onLoop(r, func() bool { r.h.dev = t.TempDir(); r.h.switchTheme(themeNames[0]); return true })
	r.waitKept(t, "theme: ")

	// a plugin whose program is not there: by its command, launched with the TUI, and fed a page
	gone := []string{"autodoc-test-no-such-plugin-program"}
	onLoop(r, func() bool {
		cmd := pluginEntry{dir: t.TempDir(), m: manifest{Name: "bycommand", Kind: "service", Command: gone}}
		launched := pluginEntry{dir: t.TempDir(), m: manifest{Name: "launched", Kind: "service", Start: "launch", Command: gone}}
		fed := pluginEntry{dir: t.TempDir(), m: manifest{Name: "fed", Kind: "service", Start: "use", Command: gone}}
		fed.m.Feed.Document = true
		r.h.pluginList = []pluginEntry{cmd, launched, fed}
		r.h.runPluginCommand("bycommand", "x")
		r.h.launchServices()
		r.h.feedDocuments()
		return true
	})
	for _, name := range []string{"bycommand", "launched", "fed"} {
		r.waitKept(t, name+" did not start: ")
	}
	if n := onLoop(r, func() int { return len(r.h.running) }); n != 0 {
		t.Errorf("%d plugins running whose program is not there", n)
	}
}

// Go to link's failure is the daemon's, said in its words: "go to link: " and its message, not the
// wire's "rpc error -32xxx:" before it.
func TestGoToLinksFailureIsTheDaemonsWords(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "see [[b]]\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	// the file's workspace is one the daemon does not serve: graph.resolve refuses it
	onLoop(r, func() bool {
		r.h.file.ws = "no-such-workspace"
		r.h.goToLink(cursorLink{raw: "b", dest: "b", wiki: true})
		return true
	})
	var got string
	r.s.WaitFor(t, "go to link's failure", func(string) bool {
		got = onLoop(r, func() string {
			for _, n := range r.h.noticeLog.items {
				if strings.HasPrefix(n.text, "go to link: ") {
					return n.text
				}
			}
			return ""
		})
		return got != ""
	})
	if strings.Contains(got, "rpc error") || got == "go to link: " {
		t.Fatalf("go to link said %q: want the daemon's message alone", got)
	}
}

// A file the default app does not open says so, and why.
func TestOpenWithTheDefaultAppSaysWhyItFailed(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool {
		r.h.browser = func(context.Context, string) error { return errors.New("no default app") }
		r.h.openWithDefaultApp()
		return true
	})
	r.waitKept(t, "open with the default app: no default app")
}
