package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// tilelayout_test.go: v0.3.0's base, a TileManager whose windows are the page (a FloatWindow, the
// fill member) and the tileable panels.

// defaultScreenGolden is the default screen as v0.2.6 drew it (the page alone, the start's toasts
// gone), captured at that release. AUTODOC_UPDATE_GOLDEN=1 writes it from the build under test.
var defaultScreenGolden = filepath.Join("testdata", "default_screen.golden")

// The default reads as before: the page tiled alone is the whole base, so the screen is v0.2.6's,
// cell for cell.
func TestTheDefaultLayoutReadsAsTheReleasedOne(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{"tui.toast.seconds": "1"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.s.WaitFor(t, "the start's toasts gone", func(sc string) bool {
		return !strings.Contains(sc, "╭") && onLoop(r, func() bool { s, w := r.h.toasts.Len(); return s == 0 && w == 0 })
	})
	sc := r.s.String()
	if os.Getenv("AUTODOC_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(defaultScreenGolden, []byte(sc), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(defaultScreenGolden)
	if err != nil {
		t.Fatal(err)
	}
	if sc != string(want) {
		t.Fatalf("the default screen differs from v0.2.6's:\n--- now\n%s\n--- v0.2.6\n%s", sc, want)
	}
}

// tilesOf is the base's manager: golib's Tiles, which the TileManager node mounts as its one child.
// Read on the loop.
func tilesOf(r *running) *widget.Tiles {
	c, _ := r.h.p.Find("tiles")
	n, ok := c.(interface{ Context() *tuicore.Context })
	if !ok || n.Context() == nil {
		return nil
	}
	for _, ch := range n.Context().Children(c) {
		if t, ok := ch.(*widget.Tiles); ok {
			return t
		}
	}
	return nil
}

// The page is the tile manager's fill member, governed from the start, and the start-up focus is
// in its editor: what is typed reaches the page.
func TestThePageIsTheTileManagersWindowFromTheStart(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	governed := onLoop(r, func() bool {
		tiles := tilesOf(r)
		return tiles != nil && tiles.Tile("editorWindow") != nil && tiles.Tile("editorWindow").Governed()
	})
	if !governed {
		t.Fatal("the page's window is not governed by the tile manager")
	}
	r.keys(t, key('i'))
	r.keys(t, decltest.Type("typed")...)
	r.s.WaitFor(t, "the typing on the page", func(string) bool { return strings.Contains(r.editorText(), "typed") })
}
