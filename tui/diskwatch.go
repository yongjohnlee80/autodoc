package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// OPEN FILES FOLLOW THE DISK (ADR 1792077464). The TUI watches the folder of every open buffer's
// file, natively where the driver can (inotify, FSEvents), by polling where it cannot, and checks
// each change against the daemon's version: a buffer with no unsaved edits reloads silently, one
// undo step, its cursor kept; a modified one is badged, and asked keep / reload / overwrite when
// it is next entered or saved, never while it is typed in; a file deleted on disk keeps its
// buffer, badged, and a save creates it again. The TUI's own saves are never outside changes.

// diskState is how a buffer's file stands on disk, as its last check found it.
type diskState uint8

const (
	diskSame    diskState = iota // the version the buffer holds, or the TUI's own save
	diskChanged                  // a newer version than the buffer's, which has unsaved edits
	diskDeleted                  // gone from disk; a save creates it again
)

// diskWatch is one watched folder: the open buffers whose files are in it.
type diskWatch struct {
	dir    string             // absolute
	gen    uint64             // this run of the watch: events and its end from an older run are dropped
	cancel context.CancelFunc // a child of h.ctx: the TUI's stop ends every watch
	refs   int                // the open buffers in dir
	mode   string             // diskWatching or diskPolling; "" while no run is up
	// at and rel are where the driver is opened and dir within it: dir's parent and its name, so the
	// driver holds the parent open, not dir, and dir's own removal ends the watch (a directory held
	// open is never reported deleted: Linux sends no IN_DELETE_SELF for it); "." in dir for a root
	at, rel string
	err     error         // why the last start failed; nil while a run is up
	wait    time.Duration // the next restart's backoff
}

// the TUI's own names for how a folder is followed (the daemon's core/follow uses the same words)
const (
	diskWatching = "watch"
	diskPolling  = "poll"
)

// diskPollEvery is vfs.Poll's interval where the folder has no native watcher.
// NOTE: deadline: a poll is a listing per interval, the only way to see a change without a watcher.
var diskPollEvery = 2 * time.Second

// diskWait collapses a burst of events for one buffer (an agent writing in steps, an editor's
// atomic save as remove+create) into one check.
// NOTE: debounce: no event says a burst is over.
const diskWait = 250 * time.Millisecond

// a folder's watch that ended or would not start is tried again, the wait doubling
const (
	diskRetryMin = time.Second
	diskRetryMax = 30 * time.Second
)

// diskReloadChoices are tui.disk_reload's values: a clean file changed on disk reloaded at once,
// or badged and asked about as a modified one is; diskReloadLabels, as Preferences shows them.
var (
	diskReloadChoices = []string{diskReloadClean, diskReloadAsk}
	diskReloadLabels  = []string{"reload them", "ask first"}
)

const (
	diskReloadClean = "clean"
	diskReloadAsk   = "ask"
)

// localDisk is the folder's driver: golib's local FS, which watches natively.
func localDisk(dir string) (vfs.FS, error) { return local.New(dir) }

// watchFile counts b in its folder's watch, starting the watch with the first buffer there. b's
// file must be known (open); a file whose folder is not known yet (its workspace not listed) is
// left unwatched.
func (h *Host) watchFile(b *openBuffer) {
	abs := h.pathOnDisk(b.file.ws, b.file.path)
	if !b.file.open || abs == "" {
		h.unwatchFile(b)
		return
	}
	dir := filepath.Dir(abs)
	if b.watch != nil && b.watch.dir == dir && b.watchName == filepath.Base(abs) {
		return // the same file: its watch already counts it
	}
	h.unwatchFile(b)
	w := h.watches[dir]
	if w == nil {
		w = &diskWatch{dir: dir, wait: diskRetryMin, at: filepath.Dir(dir), rel: filepath.Base(dir)}
		if w.at == dir { // a filesystem's root: nothing above it to hold
			w.at, w.rel = dir, "."
		}
		h.watches[dir] = w
		h.startWatch(w)
	}
	w.refs++
	b.watch, b.watchName = w, filepath.Base(abs)
}

// unwatchFile drops b from its folder's watch; the last buffer in a folder ends the watch.
func (h *Host) unwatchFile(b *openBuffer) {
	w := b.watch
	if w == nil {
		return
	}
	b.watch, b.watchName = nil, ""
	if w.refs--; w.refs > 0 {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	w.gen++ // a late event, end or restart of this watch finds a newer gen
	if h.watches[w.dir] == w {
		delete(h.watches, w.dir)
	}
}

// startWatch starts a run of w: native if the driver watches, Poll otherwise. Each event is posted
// to the loop; the channel's end is the run's end (watchEnded). A run that cannot start is tried
// again (restartWatch).
func (h *Host) startWatch(w *diskWatch) {
	w.gen++
	gen, dir := w.gen, w.dir
	ctx, cancel := context.WithCancel(h.ctx)
	w.cancel = cancel
	events, mode, closeFS, err := openWatch(ctx, h.diskFS, w.at, w.rel)
	if err != nil {
		cancel()
		if w.err == nil { // said once, not at every retry
			h.say(h.trf("autodoc.notice.diskwatch.files_in_are_not_watched_for_changes_on_disk", dir, err))
		}
		w.mode, w.err = "", err
		h.restartWatch(w)
		return
	}
	w.mode, w.err, w.wait = mode, nil, diskRetryMin
	go func() {
		for ev := range events {
			h.p.Post(func() { h.diskEvent(dir, gen, ev) })
		}
		closeFS()                                   // the run is over: its driver's handle on the folder goes with it
		h.p.Post(func() { h.watchEnded(dir, gen) }) // cancelled, or the folder went
	}()
}

// openWatch is a run's events: the native watch of rel in the driver opened at at, or a poll of it
// where the driver has none (or its watch would not start). Events name paths under at. closeFS
// releases the driver (its os.Root on the folder): the caller runs it once the events end; a run
// that does not start has released it already.
func openWatch(ctx context.Context, open func(string) (vfs.FS, error), at, rel string) (events <-chan vfs.Event, mode string, closeFS func(), err error) {
	fsys, err := open(at)
	if err != nil {
		return nil, "", nil, err
	}
	closeFS = func() {
		if c, ok := fsys.(io.Closer); ok {
			_ = c.Close()
		}
	}
	if w, ok := fsys.(vfs.Watcher); ok {
		if events, err := w.Watch(ctx, rel); err == nil {
			return events, diskWatching, closeFS, nil
		}
	}
	events, err = vfs.Poll(ctx, fsys, rel, diskPollEvery)
	if err != nil {
		closeFS()
		return nil, "", nil, err
	}
	return events, diskPolling, closeFS, nil
}

// watchEnded: a run of dir's watch is over. A cancelled run (its last buffer closed, the TUI
// stopping) is already superseded. Otherwise the folder was deleted or moved (golib closes a watch
// whose root goes): every buffer in it is checked, deleted ones badged, and the watch is started
// again with backoff while any buffer still needs the folder.
func (h *Host) watchEnded(dir string, gen uint64) {
	w := h.watches[dir]
	if w == nil || w.gen != gen {
		return
	}
	w.cancel()
	w.mode = ""
	h.checkFolder(w)
	h.restartWatch(w)
}

// restartWatch tries w again after its wait, doubling the wait up to diskRetryMax, until a run
// starts or its last buffer closes. A run that starts re-checks every buffer in the folder: what
// changed while it was not watched.
// NOTE: backoff: a folder gone stays gone until someone recreates it; nothing announces that.
func (h *Host) restartWatch(w *diskWatch) {
	wait, gen := w.wait, w.gen
	w.wait = min(w.wait*2, diskRetryMax)
	time.AfterFunc(wait, func() {
		if h.ctx.Err() != nil {
			return // the TUI stopped
		}
		h.p.Post(func() {
			if h.watches[w.dir] != w || w.gen != gen {
				return // closed, or started again meanwhile
			}
			h.startWatch(w)
			if w.mode != "" {
				h.checkFolder(w)
			}
		})
	})
}

// checkFolder asks for a check of every buffer in w's folder.
func (h *Host) checkFolder(w *diskWatch) {
	for _, b := range h.buffers {
		if b.watch == w {
			h.diskSoon(b)
		}
	}
}

// diskEvent: an event for a file asks for a check of the buffers whose file it is; an overflow, of
// every buffer in the folder. An event of an older run is dropped.
func (h *Host) diskEvent(dir string, gen uint64, ev vfs.Event) {
	w := h.watches[dir]
	if w == nil || w.gen != gen {
		return
	}
	if ev.Op == vfs.OpOverflow {
		h.checkFolder(w)
		if w.mode == diskPolling {
			// a poll's overflow is a listing that failed (the folder gone): its next good listing is
			// a new baseline, with no event for what came back. The run is started again instead,
			// with backoff, and a start checks every buffer in the folder.
			w.cancel()
			w.gen++
			w.mode = ""
			h.restartWatch(w)
		}
		return
	}
	for _, b := range h.buffers {
		if b.watch == w && vfs.Join(w.rel, b.watchName) == ev.Path {
			h.diskSoon(b)
		}
	}
}

// diskSoon is the one way to ask for a check of b: after diskWait, so an event, a stale answer and
// a save's held recheck coalesce into one diskCheck.
func (h *Host) diskSoon(b *openBuffer) {
	b.diskGen++
	gen := b.diskGen
	time.AfterFunc(diskWait, func() {
		if h.ctx.Err() != nil {
			return // the TUI stopped
		}
		h.p.Post(func() {
			if b.diskGen == gen && slices.Contains(h.buffers, b) {
				h.diskCheck(b)
			}
		})
	})
}

// diskSnap is what a check reads of b, taken on the loop: the worker never reads b.
type diskSnap struct {
	ws, path string
	raw      bool
	version  string
	saves    uint64
}

// diskRead is a check's read of the file through the daemon.
type diskRead struct {
	content, version string
	missing          bool
	err              error
}

// readDisk reads snap's file as a load does.
func readDisk(ctx context.Context, h *Host, snap diskSnap) diskRead {
	res, err := h.readFile(ctx, snap.ws, snap.path, snap.raw)
	if code(err) == rpc.CodeNotFound {
		return diskRead{missing: true}
	}
	if err != nil {
		return diskRead{err: err}
	}
	m := asMap(res)
	content, _ := m["content"].([]byte)
	return diskRead{content: string(content), version: str(m, "version")}
}

// diskCheck reads b's file through the daemon and settles what changed. Held while b's save is in
// flight (saving), and run once when the save answers (recheck).
func (h *Host) diskCheck(b *openBuffer) {
	if !b.file.open {
		return
	}
	if b.saving {
		b.recheck = true
		return
	}
	tk := h.bufferTicket(b, false) // a switch of buffer or workspace does not drop it
	snap := diskSnap{ws: b.file.ws, path: b.file.path, raw: b.file.raw, version: b.file.version, saves: b.saves}
	do(h, func(ctx context.Context) diskRead { return readDisk(ctx, h, snap) }, func(r diskRead) {
		switch {
		case !tk.Current(), !b.file.open, b.file.path != snap.path, b.file.ws != snap.ws:
			// b closed, reloaded or re-homed meanwhile: its own next event checks it
		case b.saves != snap.saves, b.file.version != snap.version:
			h.diskSoon(b) // a save started or landed while this read was out: stale; one fresh check
		case r.err != nil:
			h.keep(fmt.Errorf("checking %s on disk: %w", snap.path, r.err))
		case r.missing:
			h.markDisk(b, diskDeleted, "")
		case r.version == snap.version:
			h.markDisk(b, diskSame, "") // the TUI's own save, or a touch
		case !b.file.dirty && h.prefs.diskReload == diskReloadClean:
			h.reloadFromDisk(b, r.content, r.version)
		default:
			h.markDisk(b, diskChanged, r.version)
		}
	})
}

// markDisk badges b with how its file stands on disk (its tab and the status line); the question
// a changed file asks waits for b's next enter or save.
func (h *Host) markDisk(b *openBuffer, st diskState, version string) {
	if b.file.disk == st && b.file.diskVersion == version {
		return
	}
	b.file.disk, b.file.diskVersion = st, version
	if b == h.active {
		h.setDirty(b.file.dirty) // the status line's title, with its badge
	}
	h.syncBuffers()
	switch st {
	case diskChanged:
		h.notify(h.trf("autodoc.notice.diskwatch.changed_on_disk_your_unsaved_edits_are_kept_you", b.file.name()))
	case diskDeleted:
		h.notify(h.trf("autodoc.notice.diskwatch.was_deleted_on_disk_its_text_is_kept_saving_crea", b.file.name()))
	}
}

// askDiskOnEnter asks about b's file changed on disk, if it did, now that the user shows b: keep
// editing, reload (one u from the edits), or overwrite. Asked once per version on disk.
func (h *Host) askDiskOnEnter(b *openBuffer) {
	if b != h.active || b.file.disk != diskChanged || b.file.diskAsked == b.file.diskVersion {
		return
	}
	b.file.diskAsked = b.file.diskVersion
	h.set("App.conflictQuestion", h.trf("autodoc.text.diskwatch.changed_on_disk_while_it_had_unsaved_changes_kee", b.file.name()))
	h.open("fileConflict")
}

// reloadFromDisk puts content, the file's text at version, in b in place of what differs: each run
// of changed lines (lineHunks) replaced, from the last up, in one Batch: one undo step.
// ReplaceFollowing leaves the editing editor's cursor, Visual anchor and first shown line where
// their text went, as other editors' cursors, marks and places follow the edit: a line the disk
// did not change keeps the cursor on it. b is clean after.
func (h *Host) reloadFromDisk(b *openBuffer, content, version string) {
	ed := h.editorFor(b)
	old, now := ed.Lines(), strings.Split(content, "\n")
	if hunks := lineHunks(old, now); len(hunks) > 0 {
		ro := ed.ReadOnly()
		ed.SetReadOnly(false) // read-only to the user, not to the disk
		err := ed.Batch(func() error {
			for i := len(hunks) - 1; i >= 0; i-- { // from the last up: the lines above stay where they were
				hk := hunks[i]
				r, text := blockEdit(old, hk.oldFrom, hk.oldTo, now[hk.newFrom:hk.newTo])
				if err := ed.ReplaceFollowing(r, text); err != nil {
					return err
				}
			}
			return nil
		})
		ed.SetReadOnly(ro)
		if err != nil {
			h.keep(fmt.Errorf("reloading %s: %w", b.file.name(), err))
			return
		}
	}
	ed.MarkSaved()
	b.file.version, b.file.disk, b.file.diskVersion, b.file.diskAsked = version, diskSame, "", ""
	if b == h.active {
		h.setDirty(false)
	} else {
		b.file.dirty = false
		h.changeSoonFor(b) // no textChanged reaches the host from a headless editor
	}
	own, ok := b.ownRecovery()
	h.settleRecovery(b, own, ok)
	h.syncBuffers()
}

// hunk is a run of changed lines: old's oldFrom..oldTo replaced by now's newFrom..newTo.
type hunk struct{ oldFrom, oldTo, newFrom, newTo int }

// diffMax bounds lineHunks' work: texts differing in more lines are one hunk, their common first
// and last lines kept (a cursor in the hunk lands at its end).
const diffMax = 512

// lineHunks are the runs of lines that differ between old and now, in order: a shortest edit
// (Myers) between their middles, once their common first and last lines are set aside.
func lineHunks(old, now []string) []hunk {
	pre, oldEnd, newEnd := middleBlock(old, now)
	if pre == oldEnd && pre == newEnd {
		return nil
	}
	a, b := old[pre:oldEnd], now[pre:newEnd]
	matches, ok := myersMatches(a, b)
	if !ok {
		return []hunk{{pre, oldEnd, pre, newEnd}}
	}
	var out []hunk
	pi, pj := -1, -1
	for _, m := range append(matches, [2]int{len(a), len(b)}) {
		if m[0] > pi+1 || m[1] > pj+1 {
			out = append(out, hunk{pre + pi + 1, pre + m[0], pre + pj + 1, pre + m[1]})
		}
		pi, pj = m[0], m[1]
	}
	return out
}

// myersMatches are the lines a and b keep in common, as (i, j) pairs in order, along a shortest
// edit between them; ok is false when that edit is longer than diffMax.
func myersMatches(a, b []string) (matches [][2]int, ok bool) {
	n, m := len(a), len(b)
	lim := min(n+m, diffMax)
	off := lim + 1
	v := make([]int, 2*lim+3)
	var trace [][]int
	end := -1
	for d := 0; d <= lim && end < 0; d++ {
		trace = append(trace, slices.Clone(v)) // the ends after d-1 steps, for the way back
		for k := -d; k <= d; k += 2 {
			x := v[off+k-1] + 1
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				end = d
				break
			}
		}
	}
	if end < 0 {
		return nil, false
	}
	x, y := n, m
	for d := end; d > 0; d-- {
		pv, k := trace[d], x-y
		prev := k - 1
		if k == -d || (k != d && pv[off+k-1] < pv[off+k+1]) {
			prev = k + 1
		}
		px := pv[off+prev]
		py := px - prev
		for x > px && y > py { // the snake after the step: lines kept
			x, y = x-1, y-1
			matches = append(matches, [2]int{x, y})
		}
		x, y = px, py
	}
	for x > 0 && y > 0 {
		x, y = x-1, y-1
		matches = append(matches, [2]int{x, y})
	}
	slices.Reverse(matches)
	return matches, true
}

// middleBlock is where old and now differ: the common first lines (pre), and the ends of the lines
// between them and the common last ones, in old and in now.
func middleBlock(old, now []string) (pre, oldEnd, newEnd int) {
	for pre < len(old) && pre < len(now) && old[pre] == now[pre] {
		pre++
	}
	oldEnd, newEnd = len(old), len(now)
	for oldEnd > pre && newEnd > pre && old[oldEnd-1] == now[newEnd-1] {
		oldEnd--
		newEnd--
	}
	return pre, oldEnd, newEnd
}

// blockEdit is the edit that replaces old's lines from..to with lines. Inside the text, whole
// lines with their line breaks; reaching its end, from the end of the line before, so the last
// line's missing break stays missing.
func blockEdit(old []string, from, to int, lines []string) (widget.TextRange, string) {
	if to < len(old) {
		text := strings.Join(lines, "\n")
		if len(lines) > 0 {
			text += "\n"
		}
		return widget.TextRange{From: widget.TextPos{Line: from}, To: widget.TextPos{Line: to}}, text
	}
	const lineEnd = 1 << 30 // clamped to the line's last column
	last := widget.TextPos{Line: len(old) - 1, Col: lineEnd}
	if from == 0 {
		return widget.TextRange{To: last}, strings.Join(lines, "\n")
	}
	text := ""
	if len(lines) > 0 {
		text = "\n" + strings.Join(lines, "\n")
	}
	return widget.TextRange{From: widget.TextPos{Line: from - 1, Col: lineEnd}, To: last}, text
}
