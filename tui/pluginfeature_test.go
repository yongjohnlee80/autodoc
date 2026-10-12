package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// Every page shown raises the document's version and sends it once: a file opened, the draft after
// it closes, and a file kept as the draft when its workspace goes. The document is the page shown,
// never the snapshot read on the way to it.
func TestEveryPageShownRaisesTheFeedsVersionOnce(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "stats", "dialog", "echo", "[dialog]\nmodal = false\nwidth = 60\nheight = 6\n[feed]\ndocument = true\n")
	delay := pluginFeedDelay
	pluginFeedDelay = time.Second // a slow (-race) runner's pause between two steps stays inside it
	t.Cleanup(func() { pluginFeedDelay = delay })
	r, logs := pluginTUI(t, dir)
	time.Sleep(3 * pluginFeedDelay) // any feed the start scheduled has fired, to nobody
	onLoop(r, func() bool { r.h.plugins.openPlugin("stats"); return true })
	last := version(t, docsIn(waitSaid(t, logs, "stats", "doc "))[0])

	step := func(what, path, text string, do func()) {
		t.Helper()
		n := len(docsIn(said(t, logs, "stats")))
		onLoop(r, func() bool { do(); return true })
		r.s.WaitFor(t, what+"'s document", func(string) bool { return len(docsIn(said(t, logs, "stats"))) > n })
		time.Sleep(2 * pluginFeedDelay) // nothing more comes of it
		docs := docsIn(said(t, logs, "stats"))
		if len(docs) != n+1 {
			t.Fatalf("%s: %d documents, want one: %q", what, len(docs)-n, docs[n:])
		}
		d := docs[n]
		v := version(t, d)
		if v <= last || !strings.HasPrefix(d, fmt.Sprintf("doc %q v", path)) || !strings.Contains(d, fmt.Sprintf(" %q at ", text)) {
			t.Fatalf("%s: %q, want %q with %q and a version above %d", what, d, path, text, last)
		}
		last = v
	}
	step("a file opened", "a.md", "a\n", func() { r.h.openPath("a.md") })
	step("the file closed", "", "", r.h.closeFile)
	step("a file opened again", "a.md", "a\n", func() { r.h.openPath("a.md") })
	step("the file kept as the draft", "", "a\n", func() { r.h.setDirty(true); r.h.keepDraftOnLeave() })
}

// A plugin started mid-typing, before the pause, is sent the text as it is, with its version.
func TestAPluginStartedMidTypingHasTheTextAsItIs(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "stats", "dialog", "echo", "[dialog]\nmodal = false\nwidth = 60\nheight = 6\n[feed]\ndocument = true\n")
	r, logs := pluginTUI(t, dir)
	want := onLoop(r, func() int {
		r.h.editor.SetValue("typed just now\n")
		r.h.edited() // typing: the pause has not come, the page's snapshot is the text before
		r.h.plugins.openPlugin("stats")
		return r.h.pageVersion
	})
	got := docsIn(waitSaid(t, logs, "stats", "doc "))[0]
	if version(t, got) != want || !strings.Contains(got, `"typed just now\n"`) {
		t.Fatalf("the document at start: %q, want the typed text at version %d", got, want)
	}
}

// pluginHost's catalog and preference services take the plugins' own and refuse anything else.
func TestThePluginHostRefusesWhatIsNotThePlugins(t *testing.T) {
	r := runTUI(t, NewSession("unused", nil), Options{})
	onLoop(r, func() bool {
		c := core{r.h}
		if err := c.AddPluginEntry(Entry{ID: "file.sneaky", Source: SourcePlugin, Owner: "x", Title: "x", Run: noRun}); !errors.Is(err, ErrNotPluginEntry) {
			t.Errorf("a file.* id: %v, want ErrNotPluginEntry", err)
		}
		if err := c.AddPluginEntry(Entry{ID: "plugin.x.y", Source: SourceBuiltin, Owner: "x", Title: "x", Run: noRun}); !errors.Is(err, ErrNotPluginEntry) {
			t.Errorf("a plugin.* id from another source: %v, want ErrNotPluginEntry", err)
		}
		if err := c.AddPluginEntry(Entry{ID: "plugin.x.y", Source: SourcePlugin, Owner: "x", Title: "x: y", Run: noRun}); err != nil {
			t.Errorf("a plugin's own entry was refused: %v", err)
		}
		if ids := slices.Collect(func(yield func(string) bool) {
			for _, e := range c.PluginEntries() {
				if !yield(e.ID) {
					return
				}
			}
		}); !slices.Equal(ids, []string{"plugin.x.y"}) {
			t.Errorf("the plugin entries: %v", ids)
		}
		c.RemovePluginEntries()
		if len(c.PluginEntries()) != 0 || r.h.cat.lookup("file.open") == nil {
			t.Error("RemovePluginEntries left a plugin entry, or took another's")
		}
		if err := c.SetPluginPref("tui.theme", "dark"); !errors.Is(err, ErrNotPluginPref) {
			t.Errorf("tui.theme: %v, want ErrNotPluginPref", err)
		}
		if err := c.SetPluginPref(prefPluginPrefix+"stats"+prefPluginPlace, "top"); err != nil || r.h.prefs.pluginPlace["stats"] != "top" {
			t.Errorf("a plugin's placement: %v, placed %q", err, r.h.prefs.pluginPlace["stats"])
		}
		return true
	})
}

// stopping is what a feature started, stopped once; the theme's change is heard by those that ask.
type stopsAndThemes struct {
	fake
	stops, themes int
}

func (s *stopsAndThemes) Stopped()      { s.stops++ }
func (s *stopsAndThemes) ThemeChanged() { s.themes++ }

func TestTheRegistryTellsStopsAndThemes(t *testing.T) {
	f := &stopsAndThemes{fake: fake{name: "f"}}
	r, err := newRegistry(f)
	if err != nil {
		t.Fatal(err)
	}
	r.themeChanged()
	r.stopped()
	if f.stops != 1 || f.themes != 1 {
		t.Fatalf("stopped %d, themes %d: want one each", f.stops, f.themes)
	}
}
