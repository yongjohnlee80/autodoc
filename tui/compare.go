package tui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/diff/merge"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// compare.go: the conflict dialog's Compare. A file changed on disk while its buffer has unsaved
// edits (or a save found it so) is merged in golib's MergeView: the disk's text at the left, the
// buffer's at the right, the result between them, with the text the buffer last agreed with on disk
// (openedFile.base) as their common ancestor when it is kept, the two sides alone when it is not.
// Apply puts the result in the buffer as one undo step, unsaved, at the disk's version: the next
// save writes over that version, and a disk that moved again since is a conflict again.
//
// The dialog is a terminal's: golib's MergeView has no window's yet, so it is written into
// main.qml only when the program does not run as a window, and the conflict offers it only then.

// compareMarker is where the Compare dialog is written into main.qml.
const compareMarker = "    // ---- the conflict's Compare (a terminal's) ----\n"

//go:embed qmlcompare/*.qml
var compareDialogFS embed.FS

// withCompare is src with the Compare dialog written in after its marker, when the program does not
// run as a window; src as it is otherwise, or where the marker is missing (a test's own layout).
func withCompare(src []byte, gui bool) []byte {
	if gui {
		return src
	}
	b, err := compareDialogFS.ReadFile("qmlcompare/Compare.qml")
	if err != nil {
		panic("tui: embedded Compare.qml: " + err.Error())
	}
	text := string(b)
	at := strings.Index(text, "Dialog {")
	if at < 0 {
		panic("tui: Compare.qml has no root Dialog")
	}
	text = "Dialog {\n    id: compare" + text[at+len("Dialog {"):]
	var out strings.Builder
	for line := range strings.Lines(text) {
		if strings.TrimSpace(line) != "" {
			out.WriteString("    ")
		}
		out.WriteString(line)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		out.WriteString("\n")
	}
	return insertAfter(src, compareMarker, out.String())
}

// compareSource is the conflict's MergeSource for one buffer: the disk's text and the buffer's,
// read when the view loads, and the result put back in the buffer.
type compareSource struct {
	h *Host
	b *openBuffer
	// changed carries a change under the view: the disk's version moved, or the buffer was edited
	// (by a script, an agent through the editor socket). One slot, sent without waiting, so the
	// disk's check never waits on it; nothing is sent once the view is done (detached).
	changed  chan struct{}
	detached atomic.Bool
	quiet    bool // the source's own edit (Apply) being made: not a change under the view

	mu   sync.Mutex
	load compareLoad // what the last Load read
}

// compareLoad is what a Load read: the disk's version and text, and the buffer's revision.
type compareLoad struct {
	version, left string
	rev           uint64
	ok            bool
}

// compareView is the buffer's text and what labels it, read on the loop for a Load.
type compareView struct {
	ws, path, name string
	raw            bool
	text, base     string
	baseOK, open   bool
	rev            uint64
	disk, mine     string // the panes' labels
	gone, closed   string // what a Load says of a file gone from disk, of a buffer closed
}

func newCompareSource(h *Host, b *openBuffer) *compareSource {
	return &compareSource{h: h, b: b, changed: make(chan struct{}, 1)}
}

// onLoopWait runs fn on h's loop and waits for its answer, or for ctx.
func onLoopWait[T any](ctx context.Context, h *Host, fn func() (T, error)) (T, error) {
	type answer struct {
		v   T
		err error
	}
	ch := make(chan answer, 1)
	h.p.Post(func() {
		v, err := fn()
		ch <- answer{v, err}
	})
	select {
	case a := <-ch:
		return a.v, a.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Load reads the buffer (on the loop) and the disk: the merge's texts. A file gone from disk is an
// error naming it; the view says it, and a merge already shown stays.
func (s *compareSource) Load(ctx context.Context) (merge.Input, error) {
	v, err := onLoopWait(ctx, s.h, func() (compareView, error) {
		b := s.b
		ed := s.h.editorFor(b)
		return compareView{
			ws: b.file.ws, path: b.file.path, name: b.file.name(), raw: b.file.raw,
			text: ed.Value(), base: b.file.base, baseOK: b.file.baseOK, open: b.file.open,
			rev:  ed.Revision(),
			disk: s.h.tr("autodoc.text.compare.on_disk"), mine: s.h.tr("autodoc.text.compare.your_unsaved_edits"),
			gone:   s.h.trf("autodoc.text.compare.is_gone_from_disk", b.file.name()),
			closed: s.h.trf("autodoc.text.compare.is_no_longer_open", b.file.name()),
		}, nil
	})
	if err != nil {
		return merge.Input{}, err
	}
	if !v.open {
		return merge.Input{}, errors.New(v.closed)
	}
	r := readDisk(ctx, s.h, diskSnap{ws: v.ws, path: v.path, raw: v.raw})
	switch {
	case r.missing:
		return merge.Input{}, errors.New(v.gone)
	case r.err != nil:
		return merge.Input{}, r.err
	}
	left := asEditorText(r.content)
	s.mu.Lock()
	s.load = compareLoad{version: r.version, left: left, rev: v.rev, ok: true}
	s.mu.Unlock()
	mine := merge.Origin{Kind: merge.Buffer, Location: v.name, Revision: fmt.Sprint(v.rev), Label: v.mine}
	in := merge.Input{
		Left: left, Right: v.text,
		LeftFrom:   merge.Origin{Kind: merge.File, Location: v.path, Revision: r.version, Label: v.disk},
		RightFrom:  mine,
		ResultTo:   mine,
		ResultSide: merge.Right, // the buffer: what a conflict left unresolved keeps is the user's own
	}
	if v.baseOK {
		in.Base, in.HasBase = v.base, true
	}
	return in, nil
}

// Apply puts result in the buffer, on the loop, as one undo step, unsaved, at the disk's version
// the merge was loaded at: the disk's text that version held becomes the buffer's base. A buffer
// changed since the load is refused, the view offering to load again: the result would drop that
// change.
func (s *compareSource) Apply(ctx context.Context, result string) error {
	_, err := onLoopWait(ctx, s.h, func() (struct{}, error) {
		s.mu.Lock()
		at := s.load
		s.mu.Unlock()
		b := s.b
		ed := s.h.editorFor(b)
		if !at.ok || !b.file.open || ed.Revision() != at.rev {
			return struct{}{}, errors.New(s.h.tr("autodoc.text.compare.the_buffer_changed_reload"))
		}
		s.quiet = true
		err := s.h.putText(b, result)
		s.quiet = false
		if err != nil {
			return struct{}{}, err
		}
		b.file.setAgreed(at.version, at.left)
		b.file.disk, b.file.diskVersion, b.file.diskAsked = diskSame, "", ""
		s.detached.Store(true) // the view closes: nothing more to tell it
		s.h.syncBuffers()
		return struct{}{}, nil
	})
	return err
}

// Changed is the changes under the view (the disk's, the buffer's).
func (s *compareSource) Changed() <-chan struct{} { return s.changed }

// saw tells the view something changed under it, never waiting.
func (s *compareSource) saw() {
	if s.detached.Load() || s.quiet {
		return
	}
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// compareSaw tells an open Compare of b that b, or its file on disk, changed.
func (h *Host) compareSaw(b *openBuffer) {
	if s := h.comparing; s != nil && s.b == b {
		s.saw()
	}
}

// putText puts text in b in place of what differs, as reloadFromDisk does: each run of changed lines
// replaced, from the last up, in one Batch, one undo step. b is left unsaved.
func (h *Host) putText(b *openBuffer, text string) error {
	ed := h.editorFor(b)
	old, now := ed.Lines(), strings.Split(text, "\n")
	hunks := lineHunks(old, now)
	if len(hunks) == 0 {
		return nil
	}
	err := ed.Batch(func() error {
		for i := len(hunks) - 1; i >= 0; i-- {
			hk := hunks[i]
			r, t := blockEdit(old, hk.oldFrom, hk.oldTo, now[hk.newFrom:hk.newTo])
			if err := ed.ReplaceFollowing(r, t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if b == h.active {
		h.setDirty(true)
		h.syncBuffers()
	} else {
		h.bufferEdited(b)
	}
	return nil
}

// compare answers the conflict's Compare: it reads the disk first. A disk holding the buffer's own
// text has nothing to merge: it asks to take the disk's version, the buffer then saved at it.
// Otherwise the Compare dialog opens on the shown buffer.
func (h *Host) compare() {
	b := h.active
	tk := h.bufferTicket(b, false)
	snap := diskSnap{ws: b.file.ws, path: b.file.path, raw: b.file.raw}
	do(h, func(ctx context.Context) diskRead { return readDisk(ctx, h, snap) }, func(r diskRead) {
		switch {
		case !tk.Current(), b.file.path != snap.path, b.file.ws != snap.ws:
		case r.missing:
			h.markDisk(b, diskDeleted, "")
		case r.err != nil:
			h.failed(h.trf("autodoc.notice.compare.read", snap.path), r.err)
		case asEditorText(r.content) == h.editorFor(b).Value():
			h.askSameOnDisk(b, r.version, asEditorText(r.content))
		default:
			h.openCompare(b)
		}
	})
}

// askSameOnDisk asks to take the disk's version of b, whose text is b's own: nothing to merge.
func (h *Host) askSameOnDisk(b *openBuffer, version, text string) {
	h.ask(feature.Question{
		Title: h.tr("autodoc.text.compare.nothing_to_merge"),
		Text:  h.trf("autodoc.text.compare.the_disk_holds_your_text", b.file.name()),
		Yes:   h.tr("autodoc.text.compare.take_the_disk_s_version"),
		No:    h.tr("autodoc.text.compare.keep_editing"),
		OnYes: func() {
			if !b.file.open || h.editorFor(b).Value() != text {
				return // the buffer moved meanwhile: the question is the conflict's again at its save
			}
			b.file.setAgreed(version, text)
			b.file.disk, b.file.diskVersion, b.file.diskAsked = diskSame, "", ""
			h.editorFor(b).MarkSaved()
			if b == h.active {
				h.setDirty(false)
			} else {
				b.file.dirty = false
			}
			h.syncBuffers()
			h.notify(h.trf("autodoc.notice.compare.took_the_disk_s_version", b.file.name()))
		},
	})
}

// openCompare opens the Compare dialog on b.
func (h *Host) openCompare(b *openBuffer) {
	c, ok := h.p.Find("compareView")
	if !ok {
		return // no Compare in this layout (a window's, or a test's own)
	}
	v, ok := tuidecl.MergeViewOf(c)
	if !ok {
		return
	}
	if old := h.comparing; old != nil {
		old.detached.Store(true)
	}
	s := newCompareSource(h, b)
	h.comparing = s
	h.set("App.compareTitle", h.trf("autodoc.text.compare.title", b.file.name()))
	v.SetSource(s)
	h.open("compare")
}

// compareDone is the view closing: applied, the result in the buffer; not, back to the conflict's
// question, as Cancel does.
func (h *Host) compareDone(applied bool) {
	s := h.comparing
	if s == nil {
		return
	}
	s.detached.Store(true)
	h.comparing = nil
	h.closeDialog("compare")
	if applied {
		h.notify(h.trf("autodoc.notice.compare.merged", s.b.file.name()))
		return
	}
	h.open("fileConflict")
}

// compareClosed is the dialog closed without the view's answer (Escape): as Cancel.
func (h *Host) compareClosed() { h.compareDone(false) }
