package tui

import (
	"io/fs"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

// touchCounter is an fs.FS that counts every way a poll can touch its files: opens, reads,
// listings and stats. HotReload's poll runs on a goroutine of its own, so the count is atomic.
type touchCounter struct {
	fsys    fs.FS
	touches atomic.Int64
}

func (c *touchCounter) Open(name string) (fs.File, error) {
	c.touches.Add(1)
	return c.fsys.Open(name)
}

func (c *touchCounter) ReadFile(name string) ([]byte, error) {
	c.touches.Add(1)
	return fs.ReadFile(c.fsys, name)
}

func (c *touchCounter) ReadDir(name string) ([]fs.DirEntry, error) {
	c.touches.Add(1)
	return fs.ReadDir(c.fsys, name)
}

func (c *touchCounter) Stat(name string) (fs.FileInfo, error) {
	c.touches.Add(1)
	return fs.Stat(c.fsys, name)
}

// TestAnIdleProgramReadsNoneOfItsEmbeddedFiles: a program with a key file named follows it (HotReload
// polls four times a second), but its embedded QML and golib's themes never change while it runs.
// Over an idle second, with nothing happening, no poll touches either: each is marked Static, the
// last wrapper over it, so the mark is not hidden. Before, every poll read and hashed every embedded
// file.
func TestAnIdleProgramReadsNoneOfItsEmbeddedFiles(t *testing.T) {
	t.Parallel()
	qml, theme := &touchCounter{fsys: qmlFiles}, &touchCounter{fsys: themes.FS()}
	s := tune(t)
	s.embeddedQML, s.embeddedThemes = qml, theme
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{KeymapFile: filepath.Join(t.TempDir(), "keymap.xml")})
	r.connected(t)
	r.waitWithin(t, "the notes listed", daemonWait, func() bool { return len(r.listed()) > 0 })

	// settled: nothing of the start is still reading them
	time.Sleep(500 * time.Millisecond)
	q0, t0 := qml.touches.Load(), theme.touches.Load()
	time.Sleep(time.Second) // about four polls, with nothing happening
	if n := qml.touches.Load() - q0; n != 0 {
		t.Errorf("the embedded QML was touched %d times over an idle second, want none", n)
	}
	if n := theme.touches.Load() - t0; n != 0 {
		t.Errorf("golib's embedded themes were touched %d times over an idle second, want none", n)
	}
	if q0 == 0 || t0 == 0 {
		t.Fatalf("the program never read its embedded files through the seams (QML %d, themes %d): the cell proves nothing", q0, t0)
	}
}
