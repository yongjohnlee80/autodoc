//go:build gui

package tui

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
)

// layoutLog wraps a renderer and records, by block start, how many times each block was laid out and
// whether its last layout drew a picture (the embed drawn as its file, not its link). The editor
// lays blocks out on its loop; the test reads the log from there too, under mu all the same.
type layoutLog struct {
	guiwidget.Renderer
	mu      sync.Mutex
	laid    map[int]int
	picture map[int]bool
}

func (l *layoutLog) LayOut(b guiwidget.Block, lines []string, width float32, inside bool, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	bl := l.Renderer.LayOut(b, lines, width, inside, t, th, host)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.laid[b.From]++
	l.picture[b.From] = bl.Picture != nil
	return bl
}

func (l *layoutLog) at(line int) (laid int, picture bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.laid[line], l.picture[line]
}

// retryingNote is the note editor's renderer for notes, its retries after delays, logged.
func retryingNote(t *testing.T, notes *NoteEmbeds, chain guiwidget.Diagrammer, delays ...time.Duration) *layoutLog {
	t.Helper()
	r := notes.Renderer(chain)(NoteRendererSpec{})
	er, ok := r.(*embedRenderer)
	if !ok {
		t.Fatalf("the note renderer is %T", r)
	}
	er.retries.delays = delays
	return &layoutLog{Renderer: r, laid: map[int]int{}, picture: map[int]bool{}}
}

// paintUntil paints the editor on its loop until line's block draws a picture, or the deadline
// passes; whether it drew.
func paintUntil(onApp func(func()), paint func(), log *layoutLog, line int, within time.Duration) bool {
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		onApp(paint)
		if _, drawn := log.at(line); drawn {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// An embed whose file did not read as its line was laid out (it was being rewritten) is drawn once
// the file is there, with nothing else said: the editor keeps a block's layout while the note's text
// is unchanged, so a failed read must not be the line's last layout. On golib's own editor, painted
// natively, for a chart and a table.
func TestAnEmbedThatDidNotReadIsDrawnOnceItsFileIsThere(t *testing.T) {
	spec := `{"data":{"values":[{"a":"x","b":1}]},"mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`
	for _, c := range []struct{ name, dest, text string }{
		{"chart", "assets/sales.chart.json", spec},
		{"table", "assets/data.csv", "name,count\nclaims,3\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			notes, dir := embedNote(t, nil)
			log := retryingNote(t, notes, guiwidget.Diagrams(nil), 300*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond)
			_, onApp, paint := paintedEditor(t, log, "# Note\n\n![it]("+c.dest+")\n\nlast\n")
			onApp(paint)
			if laid, drawn := log.at(2); laid == 0 || drawn {
				t.Fatalf("with the file missing the embed was laid out %d times, drawn %v; want it laid out as its link", laid, drawn)
			}
			writeFile(t, filepath.Join(dir, filepath.FromSlash(c.dest)), c.text)
			if !paintUntil(onApp, paint, log, 2, 5*time.Second) {
				laid, _ := log.at(2)
				t.Fatalf("the file written, the embed is still its link after %d layouts: a failed read was kept as its layout", laid)
			}
		})
	}
}

// A file that never reads is asked for once per delay, then no more: a broken link costs a few
// layouts of its line, not a loop.
func TestAnEmbedThatNeverReadsStopsBeingRetried(t *testing.T) {
	notes, _ := embedNote(t, nil)
	delays := []time.Duration{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	log := retryingNote(t, notes, nil, delays...)
	_, onApp, paint := paintedEditor(t, log, "# Note\n\n![it](missing.csv)\n\nlast\n")
	for range 40 { // about 400 ms: twice the delays' sum and more
		onApp(paint)
		time.Sleep(10 * time.Millisecond)
	}
	if laid, drawn := log.at(2); laid != 1+len(delays) || drawn {
		t.Errorf("a missing file's line was laid out %d times, drawn %v; want once, then once per delay (%d)", laid, drawn, 1+len(delays))
	}
}

// A read that succeeds forgets the retries spent: a later rewrite of the same file, while the note
// stays open, is retried afresh, as the first one was, and drawn once it lands.
func TestALaterFailedReadIsRetriedAfresh(t *testing.T) {
	notes, dir := embedNote(t, nil)
	file := filepath.Join(dir, "assets", "data.csv")
	log := retryingNote(t, notes, nil, 200*time.Millisecond) // one retry each time
	editor, onApp, paint := paintedEditor(t, log, "# Note\n\n![it](assets/data.csv)\n\nlast\n")
	onApp(paint)
	writeFile(t, file, "name,count\nclaims,3\n")
	if !paintUntil(onApp, paint, log, 2, 5*time.Second) {
		t.Fatal("the first rewrite: the embed was not drawn once its file was there")
	}
	// the file goes again as the view is laid out again, and comes back
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	onApp(func() { editor.Relayout(); paint() })
	if _, drawn := log.at(2); drawn {
		t.Fatal("laid out again with its file gone, the embed is still drawn: the second rewrite was not observed")
	}
	writeFile(t, file, "name,count\nclaims,4\n")
	if !paintUntil(onApp, paint, log, 2, 5*time.Second) {
		t.Fatal("the second rewrite: the embed was not drawn once its file was back; the first rewrite's retries were not forgotten")
	}
}

// A link the exporter never reads, a URL, an absolute path or one that climbs out of the note's
// folder, is laid out once and never retried: no file it could wait for will appear.
func TestALinkTheExporterRefusesIsNeverRetried(t *testing.T) {
	notes, _ := embedNote(t, nil)
	delays := []time.Duration{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	for _, dest := range []string{"https://example.com/a.png", "/abs/a.png", "../out.png", "https://example.com/t.csv"} {
		log := retryingNote(t, notes, nil, delays...)
		_, onApp, paint := paintedEditor(t, log, "# Note\n\n![it]("+dest+")\n\nlast\n")
		for range 40 { // about 400 ms: twice the delays' sum and more
			onApp(paint)
			time.Sleep(10 * time.Millisecond)
		}
		if laid, _ := log.at(2); laid != 1 {
			t.Errorf("%s: laid out %d times, want once: the exporter refuses it without a read", dest, laid)
		}
	}
}
