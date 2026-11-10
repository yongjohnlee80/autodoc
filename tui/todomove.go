package tui

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// A TASK'S FILE FOLLOWS ITS TASK (docs/decisions/editor.md#a-tasks-file-follows-its-task). A todo
// store keeps each task in the folder of its status (open/, in-progress/, deferred/, completed/,
// archived/<year>/<month>/), so a status change, an assignment of an open task, or the refresh's
// archiving moves its file. To the disk watch that is the file deleted, whose save would write it
// again: a second file of the task, beside the one the daemon moved. So a buffer whose file is gone
// asks first, when the file was a task's, where the task is now:
//
//   - by the change log: a move the window's todo feed read (todos.changes, as auto-core's notifier
//     reads it) names the old path and the new; the feed also asks each buffer on a path a change
//     moved, rewrote or removed to be checked at once, ahead of the disk watch;
//   - by its id: todos.get of the file's name in the store holding it, which finds a move the log
//     reports only as a hand edit too.
//
// Moved, the buffer takes the new path, its text, cursor and undo kept: a clean one then takes the
// moved file's text as one undo step, as a file changed on disk does; one with unsaved edits keeps
// them, badged changed on disk, and is asked keep / reload / overwrite when it is next shown or
// saved. Removed (todos.get finds no such task), a clean buffer closes; one with unsaved edits keeps
// them, badged deleted on disk, and its save is refused: a removed task is never written again from
// a buffer. A file that is no task's is gone as before.

// todoMovesKept is how many of the latest moves the feed read are kept, to name where a gone file
// went.
const todoMovesKept = 32

// movedFile is where a gone file's task is now: its path in the same workspace (absolute for a
// file outside every workspace), and the file's text and version there.
type movedFile struct {
	path, content, version string
}

// taskFate is what became of a gone file's task: moved, removed, or neither (a file that is no
// task's, or a daemon that could not say).
type taskFate struct {
	moved   *movedFile
	removed bool
}

// todoFilesChanged takes the changes the feed read to the open buffers: each move is kept to name
// where its file went, and every buffer on a path a change moved, rewrote or removed is checked at
// once. A check of a file that did not change does nothing.
func (h *Host) todoFilesChanged(cs []todoFileChange) {
	for _, c := range cs {
		if c.to != "" && c.to != c.from {
			h.todoMoves = append(h.todoMoves, c)
			if n := len(h.todoMoves); n > todoMovesKept {
				h.todoMoves = slices.Delete(h.todoMoves, 0, n-todoMovesKept)
			}
		}
		for _, b := range h.buffers {
			if b.file.open && c.of(h.pathOnDisk(b.file.ws, b.file.path)) {
				h.diskSoon(b)
			}
		}
	}
}

// of reports whether the change is of the file at abs: its store's path joined with its from, the
// two canonical, is abs's canonical path. A path that only ends as from does is another store's.
func (c todoFileChange) of(abs string) bool {
	if c.store == "" || !endsWithPath(abs, c.from) { // the cheap test first
		return false
	}
	return canonicalFile(abs) == canonicalFile(filepath.Join(filepath.FromSlash(c.store), filepath.FromSlash(c.from)))
}

// canonicalFile is abs with its folder's symlinks resolved, as a store's path is: the folder of a
// gone file still exists.
func canonicalFile(abs string) string {
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(dir, filepath.Base(abs))
	}
	return abs
}

// todoMoveOf is the latest move the feed read of the file at abs, in its own store, nil for none.
func (h *Host) todoMoveOf(abs string) *todoFileChange {
	for i := len(h.todoMoves) - 1; i >= 0; i-- {
		if c := h.todoMoves[i]; c.of(abs) {
			return &c
		}
	}
	return nil
}

// taskIDOf is the id a task file's frontmatter gives, "" for none.
func taskIDOf(content string) string {
	fm, err := frontmatterOf(content)
	if err != nil {
		return ""
	}
	switch id := feature.AsMap(fm)["id"].(type) {
	case string:
		return id
	case int64, int, uint64, float64:
		return fmt.Sprint(id)
	}
	return ""
}

// endsWithPath reports whether the path abs ends with the slash-separated relative path rel.
func endsWithPath(abs, rel string) bool {
	return abs != "" && rel != "" && strings.HasSuffix(filepath.ToSlash(abs), "/"+rel)
}

// taskBuckets are the folders of a store a task's file lies in: a status's, or an archive month's.
var (
	taskBuckets = []string{"open", "in-progress", "deferred", "completed"}
	yearMonth   = regexp.MustCompile(`^[0-9]{4}/[0-9]{2}$`)
)

// taskShaped reports whether abs lies where a task's file does in a store: <bucket>/<id>.md, or
// archived/<year>/<month>/<id>.md. Only such a file is asked about.
func taskShaped(abs string) bool {
	parts := strings.Split(filepath.ToSlash(abs), "/")
	n := len(parts)
	if n < 2 || !strings.HasSuffix(parts[n-1], ".md") {
		return false
	}
	return slices.Contains(taskBuckets, parts[n-2]) || n >= 4 && parts[n-4] == "archived" && yearMonth.MatchString(parts[n-3]+"/"+parts[n-2])
}

// taskFateOf is what became of the task whose file p of workspace ws, at abs on disk, is gone. Off
// the loop: first the move the feed read (hint), then the daemon: todos.stores for the store
// holding abs, todos.get for the task, whose file is named by its id.
func (h *Host) taskFateOf(ctx context.Context, ws, p, abs string, raw bool, hint *todoFileChange) taskFate {
	if abs == "" || !taskShaped(abs) {
		return taskFate{}
	}
	slashP := filepath.ToSlash(p)
	// the task a file of a store holds is named by its file: the file it moved to is that task's
	// only when its own id says so
	id := strings.TrimSuffix(path.Base(slashP), ".md")
	if hint != nil && (slashP == hint.from || strings.HasSuffix(slashP, "/"+hint.from)) {
		if m, ok := h.readMoved(ctx, ws, strings.TrimSuffix(slashP, hint.from)+hint.to, raw); ok && taskIDOf(m.content) == id {
			return taskFate{moved: &m}
		}
	}
	canon := canonicalFile(abs) // the store's path is canonical
	res, err := h.call(ctx, "todos.stores")
	if err != nil {
		return taskFate{}
	}
	for _, s := range feature.AsList(res) {
		sm := feature.AsMap(s)
		roots := feature.AsList(sm["roots"])
		rel, ok := under(feature.Str(sm, "path"), canon)
		if !ok || len(roots) == 0 {
			continue
		}
		root, _ := roots[0].(string)
		got, err := h.call(ctx, "todos.get", map[string]any{"root": root}, id)
		if feature.Code(err) == codeNoTask {
			return taskFate{removed: true}
		}
		if err != nil {
			return taskFate{}
		}
		now := feature.Str(feature.AsMap(got), "path")
		if now == "" || now == rel || !(slashP == rel || strings.HasSuffix(slashP, "/"+rel)) {
			return taskFate{}
		}
		if m, ok := h.readMoved(ctx, ws, strings.TrimSuffix(slashP, rel)+now, raw); ok && taskIDOf(m.content) == id {
			return taskFate{moved: &m}
		}
		return taskFate{}
	}
	return taskFate{}
}

// codeNoTask is the todo system's "no such task" (core/todo's CodeNoTask).
const codeNoTask int64 = -32072

// readMoved reads the file a task moved to, its slash-separated path to in workspace ws (absolute
// for none).
func (h *Host) readMoved(ctx context.Context, ws, to string, raw bool) (movedFile, bool) {
	if ws == "" {
		to = filepath.FromSlash(to)
	}
	read, err := h.readFile(ctx, ws, to, raw)
	if err != nil {
		return movedFile{}, false
	}
	m := feature.AsMap(read)
	content, _ := m["content"].([]byte)
	return movedFile{path: to, content: string(content), version: feature.Str(m, "version")}, true
}

// under is abs's path inside dir, with / separators, when abs is in dir.
func under(dir, abs string) (string, bool) {
	if dir == "" {
		return "", false
	}
	rel, err := filepath.Rel(filepath.FromSlash(dir), abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// followTask settles b, whose file is gone, by what became of its task; false when nothing is known
// of it (no task's file), for the caller's own handling of a file deleted.
func (h *Host) followTask(b *openBuffer, f taskFate) bool {
	switch {
	case f.moved != nil:
		h.followMove(b, *f.moved)
	case f.removed && !b.file.dirty:
		name := b.file.name()
		h.dropBuffer(b)
		h.notify(h.trf("autodoc.notice.todomove.removed_closed", name))
	case f.removed:
		h.keepTaskGone(b, h.trf("autodoc.notice.todomove.removed_kept", b.file.name()))
	default:
		return false
	}
	return true
}

// keepTaskGone keeps b, whose task's file is gone from its path for good, badged deleted on disk:
// its save is refused (writeAs), never a second file of the task. note says why.
func (h *Host) keepTaskGone(b *openBuffer, note string) {
	b.file.taskGone = true
	b.file.disk, b.file.diskVersion = diskDeleted, ""
	if b == h.active {
		h.setDirty(b.file.dirty) // the status line's title, with its badge
	}
	h.syncBuffers()
	h.notify(note)
}

// followMove makes b the buffer of the file its task moved to, keeping its text, cursor and undo
// (adopt); then, clean and set to reload, it takes the moved file's text as one undo step, else it
// is badged changed on disk, asked about when next shown or saved. Either way it says where the
// file went. A buffer already open on the new path is that file's: b, clean, closes; with unsaved
// edits, it keeps them where it is, its save refused.
func (h *Host) followMove(b *openBuffer, to movedFile) {
	from, dirty := b.file.name(), b.file.dirty
	if o := h.bufferOf(feature.FileRef{WS: b.file.ws, Path: to.path}); o != nil && o != b {
		if !dirty {
			h.dropBuffer(b)
			h.notify(h.trf("autodoc.notice.todomove.moved_to", from, to.path))
			return
		}
		h.keepTaskGone(b, h.trf("autodoc.notice.todomove.moved_to_open", from, to.path))
		return
	}
	h.adopt(b, b.file.ws, to.path, b.file.version, b.file.raw, nil)
	b.file.dirty = dirty // adopt with no text takes it as unsaved; it is as it was
	if !dirty && h.prefs.diskReload == diskReloadClean {
		h.reloadFromDisk(b, to.content, to.version)
		h.notify(h.trf("autodoc.notice.todomove.moved_to", from, b.file.name()))
		return
	}
	// markDisk's own notice would say only that it changed: this one says where it went
	b.file.disk, b.file.diskVersion = diskChanged, to.version
	if b == h.active {
		h.setDirty(dirty) // the status line's title, with its badge
	}
	h.syncBuffers()
	h.notify(h.trf("autodoc.notice.todomove.moved_to_asked", from, b.file.name()))
}
