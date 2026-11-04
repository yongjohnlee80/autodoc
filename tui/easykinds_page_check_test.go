package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// A gate-only check, not for merging: each of golib's cell kinds drawn in a real terminal page (a
// note's Rendered view), a mind map as the control, a flowchart's notice; each screen is logged.
func TestTheEasyKindsDrawInATerminalNotesPage(t *testing.T) {
	notes := []struct {
		file, src string
		drawn     []string // what the page draws
		source    []string // the fence's text, never on the page
	}{
		{"timeline.md", "# Timeline\n\n```mermaid\ntimeline\n  title Social media\n  section Early\n    2002 : LinkedIn\n    2004 : Facebook : Google\n  section Later\n    2005 : YouTube\n```\n",
			[]string{"┃ Early ┃", "│ 2002 ├", "┤ LinkedIn │", "┤ Google │", "▼"}, []string{"section Early", "2004 : Facebook"}},
		{"journey.md", "# Journey\n\n```mermaid\njourney\n  title My working day\n  section Go to work\n    Make tea: 5: Me\n    Do work: 1: Me, Cat\n```\n",
			[]string{"┃ Go to work ┃", "┤ Make tea │", "●●●●● 5  Me", "●○○○○ 1  Me, Cat"}, []string{"Make tea: 5", "section Go"}},
		{"sequence.md", "# Sequence\n\n```mermaid\nsequenceDiagram\n  participant A as Alice\n  participant B as Bob\n  A->>+B: Hello Bob\n  B-->>-A: Hi\n  Note over A,B: shared\n```\n",
			[]string{"│ Alice │", "│ Bob │", "Hello Bob", "▶┨", "├◀╌", "│ shared"}, []string{"A->>+B", "Note over"}},
		{"mindmap.md", "# Mind map\n\n```mermaid\nmindmap\n  root((Larder))\n    Shelf\n    Bin\n```\n",
			[]string{"Larder", "Shelf", "Bin", "╭"}, []string{"root((Larder))"}},
		{"flowchart.md", "# Flowchart\n\n```mermaid\nflowchart LR\n  A-->B\n```\n",
			[]string{"flowchart diagram: not drawn in the terminal (Raw shows its source)"}, []string{"A-->B"}},
	}
	files := []string{}
	for _, n := range notes {
		files = append(files, n.file, n.src)
	}
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, files...)}, tableInclude)
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 60)
	r.waitAttached(t, "kb")
	page := func() bool {
		return onLoop(r, func() bool {
			e := r.h.editor.(*widget.Editor)
			_, ok := e.DocumentView().(*notePageView)
			return ok && e.ShownView() == widget.ViewRendered
		})
	}
	for _, n := range notes {
		onLoop(r, func() bool { r.h.openPath(n.file); return true })
		r.waitFile(t, n.file)
		r.s.WaitFor(t, n.file+" shown as its page", func(string) bool { return page() })
		for _, want := range n.drawn {
			r.s.WaitForText(t, want)
		}
		screen := r.s.String()
		t.Logf("%s, Rendered:\n%s", n.file, screen)
		for _, src := range n.source {
			if strings.Contains(screen, src) {
				t.Errorf("%s: the page shows the fence's source %q", n.file, src)
			}
		}
	}
}
