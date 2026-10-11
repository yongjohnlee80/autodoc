package todo

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
)

const (
	ownerMarker = ".autodoc-owner" // in a claimed store's directory
	trashDir    = ".trash"         // a staged delete's file, until it is removed
)

var (
	yearRe  = regexp.MustCompile(`^[0-9]{4}$`)
	monthRe = regexp.MustCompile(`^[0-9]{2}$`)
)

// store is one open store: its filesystem, its documents, and the mutex that serializes every
// read and write of it.
type store struct {
	id   string
	path string
	fsys vfs.FS
	docs *docs.Docs

	mu        sync.Mutex
	recovered bool
	cache     map[string]cached // the active buckets' files, by path; archives are never cached
}

// cached is an active file as last read: the task, or why it is not one.
type cached struct {
	version vfs.Version
	task    Task
	bad     string
}

// eligible is every file the store's documents may name: a task file in a bucket or an archive
// partition, a staged delete in the trash, and the owner marker.
func eligible(p string) bool {
	if p == ownerMarker {
		return true
	}
	if !strings.HasSuffix(p, ".md") {
		return false
	}
	parts := strings.Split(p, "/")
	switch len(parts) {
	case 2:
		return slices.Contains(activeBuckets, parts[0]) || parts[0] == trashDir || parts[0] == "automated"
	case 4:
		return parts[0] == "archived" && yearRe.MatchString(parts[1]) && monthRe.MatchString(parts[2])
	}
	return false
}

// open is an At's store, opened once per Manager and recovered before its first use. An
// interrupted store move is settled first: it decides which directory is the store.
func (m *Manager) open(ctx context.Context, w where) (*store, error) {
	if err := m.recoverMoves(ctx, w.rec.ID, false); err != nil {
		return nil, err
	}
	st, _ := m.j.Settings(ctx)
	path := w.rec.Path
	if rec, ok := st.Stores[w.rec.ID]; ok {
		path = rec.Path
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.stores[w.rec.ID]; s != nil && s.path == path {
		return s, nil
	} else if s != nil {
		s.fsys.Close() // the store moved
		delete(m.stores, w.rec.ID)
	}
	fsys, canon, err := m.disk.Open(ctx, path)
	if err != nil {
		if errors.Is(err, vfs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s does not exist (todos.scaffold creates it)", ErrNotAStore, path)
		}
		return nil, err
	}
	if canon != path {
		fsys.Close()
		return nil, fmt.Errorf("%w: %s resolves to %s", ErrNotWritable, path, canon)
	}
	if !isStore(ctx, fsys, path) {
		fsys.Close()
		return nil, fmt.Errorf("%w: %s has no buckets (todos.scaffold creates them)", ErrNotAStore, path)
	}
	s := &store{id: w.rec.ID, path: path, fsys: fsys, docs: docs.New(fsys, eligible), cache: map[string]cached{}}
	m.stores[w.rec.ID] = s
	return s, nil
}

// isStore reports a directory that is a todo store: named .todo-list, or holding a bucket or the
// owner marker.
func isStore(ctx context.Context, fsys vfs.FS, dir string) bool {
	if path.Base(dir) == todoDir {
		return true
	}
	for _, name := range append(slices.Clone(Statuses), ownerMarker) {
		if _, err := fsys.Stat(ctx, name); err == nil {
			return true
		}
	}
	return false
}

// ready runs recovery once, before the store serves anything. The caller holds st.mu.
func (m *Manager) ready(ctx context.Context, st *store) error {
	if st.recovered {
		return nil
	}
	rows, err := m.j.Pending(ctx, st.id)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if c.Op == OpStoreMove {
			continue
		}
		if err := m.recover(ctx, st, c); err != nil {
			return err
		}
	}
	st.recovered = true
	return nil
}

// mark writes the owner marker when it is missing. The caller holds st.mu.
func (st *store) mark(ctx context.Context, at time.Time) error {
	if _, err := st.fsys.Stat(ctx, ownerMarker); err == nil {
		return nil
	}
	_, err := st.docs.Write(ctx, ownerMarker, []byte("claimed by autodoc at "+stamp(at)+"\n"), "")
	if errors.Is(err, vfs.ErrConflict) {
		return nil
	}
	return err
}

// unmark removes the owner marker. The caller holds st.mu.
func (st *store) unmark(ctx context.Context) error {
	doc, err := st.docs.Read(ctx, ownerMarker)
	if errors.Is(err, vfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return st.docs.Remove(ctx, ownerMarker, doc.Version)
}

// file is one task file found by a scan: the task, or why it is not one.
type file struct {
	path string
	task Task
	bad  string
}

// scanActive reads the active buckets, through the cache: a file whose version is unchanged is
// not read again. archived/ is not touched. The caller holds st.mu.
func (m *Manager) scanActive(ctx context.Context, st *store) ([]file, error) {
	var out []file
	seen := map[string]bool{}
	for _, b := range append(slices.Clone(activeBuckets), "automated") {
		entries, bad, err := st.list(ctx, b)
		if err != nil {
			return nil, err
		}
		out = append(out, bad...)
		for _, fi := range entries {
			seen[fi.Path] = true
			if b == "automated" { // the removed bucket: its files are listed, never read
				out = append(out, file{path: fi.Path, bad: automationRemoved + ": bucket 'automated'"})
				continue
			}
			if c, ok := st.cache[fi.Path]; ok && c.version == fi.Version {
				out = append(out, file{path: fi.Path, task: c.task, bad: c.bad})
				continue
			}
			f, version := st.load(ctx, fi.Path)
			if version != "" {
				st.cache[fi.Path] = cached{version: version, task: f.task, bad: f.bad}
			}
			out = append(out, f)
		}
	}
	for p := range st.cache {
		if !seen[p] {
			delete(st.cache, p)
		}
	}
	return out, nil
}

// readPartition reads one archive partition, "archived/2026/05", whole and uncached.
func (m *Manager) readPartition(ctx context.Context, st *store, dir string) ([]file, error) {
	entries, out, err := st.list(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, fi := range entries {
		f, _ := st.load(ctx, fi.Path)
		out = append(out, f)
	}
	return out, nil
}

// list is a bucket's .md files, by name; a symlink or another non-regular file is reported, never
// followed. A bucket that is itself a symlink is reported and not read.
func (st *store) list(ctx context.Context, dir string) ([]vfs.FileInfo, []file, error) {
	fi, err := st.fsys.Stat(ctx, dir)
	if errors.Is(err, vfs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !fi.IsDir() {
		return nil, []file{{path: dir, bad: "a bucket that is not a directory is not read (a symlink is never followed)"}}, nil
	}
	entries, err := st.fsys.ReadDir(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	var files []vfs.FileInfo
	var bad []file
	for _, e := range entries {
		if strings.HasPrefix(e.Name, ".") || !strings.HasSuffix(e.Name, ".md") {
			continue
		}
		if !e.IsRegular() {
			bad = append(bad, file{path: e.Path, bad: "not a regular file (a symlink is never followed)"})
			continue
		}
		files = append(files, e)
	}
	return files, bad, nil
}

// load reads and decodes one file, with the version its bytes were read at.
func (st *store) load(ctx context.Context, p string) (file, vfs.Version) {
	doc, err := st.docs.Read(ctx, p)
	if err != nil {
		return file{path: p, bad: "read: " + err.Error()}, ""
	}
	t, err := decode(doc.Content)
	if err != nil {
		return file{path: p, bad: err.Error()}, doc.Version
	}
	t.Path, t.Rev = p, rev(doc.Content)
	return file{path: p, task: t}, doc.Version
}

// partitions are the archive's months, newest first: "2026/10", "2026/09", ….
func (st *store) partitions(ctx context.Context) ([]string, error) {
	years, err := st.dirs(ctx, "archived", yearRe)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, y := range slices.Backward(years) {
		months, err := st.dirs(ctx, "archived/"+y, monthRe)
		if err != nil {
			return nil, err
		}
		for _, mo := range slices.Backward(months) {
			out = append(out, y+"/"+mo)
		}
	}
	return out, nil
}

// dirs are dir's subdirectories whose names match re, sorted. A missing dir has none.
func (st *store) dirs(ctx context.Context, dir string, re *regexp.Regexp) ([]string, error) {
	entries, err := st.fsys.ReadDir(ctx, dir)
	if errors.Is(err, vfs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && re.MatchString(e.Name) {
			out = append(out, e.Name)
		}
	}
	return out, nil
}

// find is the path of id's file: the active buckets, then the archive's partitions newest first,
// stopping at the first hit. Nothing is read; each candidate is one stat.
func (st *store) find(ctx context.Context, id string) (string, error) {
	if !safeID(id) {
		return "", fmt.Errorf("%w: id %q", ErrInvalid, id)
	}
	for _, b := range activeBuckets {
		if fi, err := st.fsys.Stat(ctx, b+"/"+id+".md"); err == nil && fi.IsRegular() {
			return fi.Path, nil
		}
	}
	parts, err := st.partitions(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range parts {
		if fi, err := st.fsys.Stat(ctx, "archived/"+p+"/"+id+".md"); err == nil && fi.IsRegular() {
			return fi.Path, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, id)
}

// current reads id's file fresh: the task, the version its bytes were read at, and a rev check
// against what the client saw. The caller holds st.mu.
func (st *store) current(ctx context.Context, id, seen string) (Task, vfs.Version, error) {
	p, err := st.find(ctx, id)
	if err != nil {
		return Task{}, "", err
	}
	doc, err := st.docs.Read(ctx, p)
	if err != nil {
		return Task{}, "", err
	}
	if r := rev(doc.Content); r != seen {
		return Task{}, "", fmt.Errorf("%w: %s", ErrStale, id)
	}
	t, err := decode(doc.Content)
	if err != nil {
		return Task{}, "", fmt.Errorf("%w: %s is malformed: %w", ErrInvalid, p, err)
	}
	t.Path, t.Rev = p, rev(doc.Content)
	return t, doc.Version, nil
}

// diff logs the edits made outside the daemon since the snapshot, among files read from the paths
// inScope admits: a changed file is an update naming the fields that changed, a file that moved is
// an update too, a vanished one a remove, and in the active buckets a new one an add. None is ever
// an assign. The first sight of a store takes its active snapshot silently; an archived file the
// snapshot never had is not news. The caller holds st.mu.
func (m *Manager) diff(ctx context.Context, st *store, files []file, inScope func(string) bool, active bool) {
	if err := m.diffErr(ctx, st, files, inScope, active); err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.diff.failed", "store": st.id})
	}
}

func (m *Manager) diffErr(ctx context.Context, st *store, files []file, inScope func(string) bool, active bool) error {
	rows, err := m.j.Snapshot(ctx, st.id)
	if err != nil {
		return err
	}
	if len(rows) == 0 && active {
		head, err := m.j.Head(ctx, st.id)
		if err != nil {
			return err
		}
		if head == 0 {
			var base []SnapshotRow
			for _, f := range files {
				if f.bad == "" {
					base = append(base, rowOf(f.task))
				}
			}
			return m.j.Baseline(ctx, st.id, base)
		}
	}
	byPath := map[string]SnapshotRow{}
	for _, r := range rows {
		if inScope(r.Path) {
			byPath[r.Path] = r
		}
	}
	onDisk := map[string]bool{}
	for _, f := range files {
		onDisk[f.path] = true
	}
	moved := map[string]bool{} // rows whose task turned up at another path
	now := m.now()
	hand := func(c Change, e Edit) error {
		c.Store, c.Hand, c.State, c.At = st.id, true, StateCommitted, now
		e.Store = st.id
		_, err := m.j.Record(ctx, c, e)
		return err
	}
	for _, f := range files {
		if f.bad != "" {
			continue
		}
		t := f.task
		row := rowOf(t)
		if old, ok := byPath[f.path]; ok {
			if old.Rev != t.Rev {
				err := hand(Change{ID: t.ID, Kind: KindUpdate, Op: OpInPlace, Fields: changedFields(t, old.Fields, row.Fields),
					FromPath: f.path, ToPath: f.path, FromRev: old.Rev, ToRev: t.Rev, From: old.Status, To: t.Status}, Edit{Put: []SnapshotRow{row}})
				if err != nil {
					return err
				}
			}
			continue
		}
		var prior *SnapshotRow
		for _, r := range rows {
			if r.ID == t.ID && !onDisk[r.Path] && !moved[r.Path] && (inScope(r.Path) || !st.exists(ctx, r.Path)) {
				prior = &r
				break
			}
		}
		if prior != nil {
			moved[prior.Path] = true
			err := hand(Change{ID: t.ID, Kind: KindUpdate, Op: OpMove, Fields: changedFields(t, prior.Fields, row.Fields),
				FromPath: prior.Path, ToPath: f.path, FromRev: prior.Rev, ToRev: t.Rev, From: prior.Status, To: t.Status},
				Edit{Put: []SnapshotRow{row}, Delete: []string{prior.Path}})
			if err != nil {
				return err
			}
			continue
		}
		if !active {
			continue
		}
		if err := hand(Change{ID: t.ID, Kind: KindAdd, Op: OpCreate, ToPath: f.path, ToRev: t.Rev, To: t.Status}, Edit{Put: []SnapshotRow{row}}); err != nil {
			return err
		}
	}
	for p, r := range byPath {
		if onDisk[p] || moved[p] {
			continue
		}
		if malformedAt(files, p) {
			continue
		}
		// gone from here: archived elsewhere by another writer, or removed
		if np, err := st.find(ctx, r.ID); err == nil && !inScope(np) {
			if f, _ := st.load(ctx, np); f.bad == "" {
				row := rowOf(f.task)
				err := hand(Change{ID: r.ID, Kind: KindUpdate, Op: OpMove, Fields: changedFields(f.task, r.Fields, row.Fields),
					FromPath: p, ToPath: np, FromRev: r.Rev, ToRev: f.task.Rev, From: r.Status, To: f.task.Status},
					Edit{Put: []SnapshotRow{row}, Delete: []string{p}})
				if err != nil {
					return err
				}
				continue
			}
		}
		if err := hand(Change{ID: r.ID, Kind: KindRemove, Op: OpDelete, FromPath: p, FromRev: r.Rev, From: r.Status}, Edit{Delete: []string{p}}); err != nil {
			return err
		}
	}
	return nil
}

func malformedAt(files []file, p string) bool {
	for _, f := range files {
		if f.path == p {
			return f.bad != ""
		}
	}
	return false
}

func (st *store) exists(ctx context.Context, p string) bool {
	_, err := st.fsys.Stat(ctx, p)
	return err == nil
}

func activePath(p string) bool {
	b, _, _ := strings.Cut(p, "/")
	return slices.Contains(activeBuckets, b)
}
