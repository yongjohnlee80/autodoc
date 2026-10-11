// Package todotest has in-memory stand-ins for core/todo's ports: a Journal, a disk of
// directories over golib's memfs, and a workspace table. Each can fail on demand, so a test can
// stop a mutation at any step and recover from what is left.
package todotest

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// Roots is a workspace table: name to root.
type Roots map[string]string

func (r Roots) Root(name string) (string, bool) { v, ok := r[name]; return v, ok }
func (r Roots) Names() []string                 { return slices.Sorted(maps.Keys(r)) }

// ErrCrash is what a Fail or Fault hook returns to stop an operation as a crash would.
var ErrCrash = errors.New("todotest: crash")

// Journal is an in-memory todo.Journal. Each method is one transaction: it applies whole or not
// at all.
type Journal struct {
	mu        sync.Mutex
	seq       int64
	rows      map[int64]todo.Change
	snap      map[string]map[string]todo.SnapshotRow
	consumers map[string]map[string]todo.Consumer
	settings  todo.Settings

	// Fail, when set, is called with a method's name before it does anything; an error is that
	// method's result and changes nothing.
	Fail func(op string) error
}

// NewJournal is an empty journal.
func NewJournal() *Journal {
	return &Journal{rows: map[int64]todo.Change{}, snap: map[string]map[string]todo.SnapshotRow{},
		consumers: map[string]map[string]todo.Consumer{}}
}

var _ todo.Journal = (*Journal)(nil)

func (j *Journal) enter(op string) error {
	j.mu.Lock()
	if j.Fail != nil {
		if err := j.Fail(op); err != nil {
			j.mu.Unlock()
			return err
		}
	}
	return nil
}

func (j *Journal) Begin(_ context.Context, c todo.Change) (int64, error) {
	if err := j.enter("Begin"); err != nil {
		return 0, err
	}
	defer j.mu.Unlock()
	j.seq++
	c.Seq, c.State = j.seq, todo.StatePending
	j.rows[c.Seq] = c
	return c.Seq, nil
}

func (j *Journal) Stage(_ context.Context, seq int64) error {
	if err := j.enter("Stage"); err != nil {
		return err
	}
	defer j.mu.Unlock()
	c, ok := j.rows[seq]
	if !ok || c.State != todo.StatePending {
		return fmt.Errorf("todotest: stage %d: not pending", seq)
	}
	c.State = todo.StateStaged
	j.rows[seq] = c
	return nil
}

func (j *Journal) Commit(_ context.Context, seq int64, e todo.Edit) error {
	if err := j.enter("Commit"); err != nil {
		return err
	}
	defer j.mu.Unlock()
	c, ok := j.rows[seq]
	if !ok || c.State == todo.StateCommitted {
		return fmt.Errorf("todotest: commit %d: not pending", seq)
	}
	c.State = todo.StateCommitted
	j.rows[seq] = c
	j.apply(e)
	return nil
}

func (j *Journal) Drop(_ context.Context, seq int64) error {
	if err := j.enter("Drop"); err != nil {
		return err
	}
	defer j.mu.Unlock()
	c, ok := j.rows[seq]
	if !ok || c.State == todo.StateCommitted {
		return fmt.Errorf("todotest: drop %d: not pending", seq)
	}
	delete(j.rows, seq)
	return nil
}

func (j *Journal) Pending(_ context.Context, store string) ([]todo.Change, error) {
	if err := j.enter("Pending"); err != nil {
		return nil, err
	}
	defer j.mu.Unlock()
	return j.select_(store, func(c todo.Change) bool { return c.State != todo.StateCommitted }), nil
}

func (j *Journal) Record(_ context.Context, c todo.Change, e todo.Edit) (int64, error) {
	if err := j.enter("Record"); err != nil {
		return 0, err
	}
	defer j.mu.Unlock()
	j.seq++
	c.Seq, c.State = j.seq, todo.StateCommitted
	j.rows[c.Seq] = c
	j.apply(e)
	return c.Seq, nil
}

func (j *Journal) Head(_ context.Context, store string) (int64, error) {
	if err := j.enter("Head"); err != nil {
		return 0, err
	}
	defer j.mu.Unlock()
	var head int64
	for _, c := range j.rows {
		if c.Store == store && c.State == todo.StateCommitted {
			head = max(head, c.Seq)
		}
	}
	return head, nil
}

func (j *Journal) After(_ context.Context, store string, after int64, limit int) ([]todo.Change, error) {
	if err := j.enter("After"); err != nil {
		return nil, err
	}
	defer j.mu.Unlock()
	out := j.select_(store, func(c todo.Change) bool { return c.State == todo.StateCommitted && c.Seq > after })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (j *Journal) Prune(_ context.Context, store string, before time.Time, upTo int64) (int, error) {
	if err := j.enter("Prune"); err != nil {
		return 0, err
	}
	defer j.mu.Unlock()
	n := 0
	for seq, c := range j.rows {
		if c.Store == store && c.State == todo.StateCommitted && c.At.Before(before) && seq <= upTo {
			delete(j.rows, seq)
			n++
		}
	}
	return n, nil
}

func (j *Journal) Snapshot(_ context.Context, store string) ([]todo.SnapshotRow, error) {
	if err := j.enter("Snapshot"); err != nil {
		return nil, err
	}
	defer j.mu.Unlock()
	rows := slices.Collect(maps.Values(j.snap[store]))
	slices.SortFunc(rows, func(a, b todo.SnapshotRow) int { return cmp.Compare(a.Path, b.Path) })
	return rows, nil
}

func (j *Journal) Baseline(_ context.Context, store string, rows []todo.SnapshotRow) error {
	if err := j.enter("Baseline"); err != nil {
		return err
	}
	defer j.mu.Unlock()
	j.snap[store] = map[string]todo.SnapshotRow{}
	for _, r := range rows {
		j.snap[store][r.Path] = r
	}
	return nil
}

func (j *Journal) Consumers(_ context.Context, store string) ([]todo.Consumer, error) {
	if err := j.enter("Consumers"); err != nil {
		return nil, err
	}
	defer j.mu.Unlock()
	out := slices.Collect(maps.Values(j.consumers[store]))
	slices.SortFunc(out, func(a, b todo.Consumer) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

func (j *Journal) PutConsumer(_ context.Context, store string, c todo.Consumer) error {
	if err := j.enter("PutConsumer"); err != nil {
		return err
	}
	defer j.mu.Unlock()
	if j.consumers[store] == nil {
		j.consumers[store] = map[string]todo.Consumer{}
	}
	j.consumers[store][c.Name] = c
	return nil
}

func (j *Journal) Settings(_ context.Context) (todo.Settings, error) {
	if err := j.enter("Settings"); err != nil {
		return todo.Settings{}, err
	}
	defer j.mu.Unlock()
	return copySettings(j.settings), nil
}

func (j *Journal) UpdateSettings(_ context.Context, fn func(*todo.Settings) error) error {
	if err := j.enter("UpdateSettings"); err != nil {
		return err
	}
	defer j.mu.Unlock()
	s := copySettings(j.settings)
	if err := fn(&s); err != nil {
		return err
	}
	j.settings = s
	return nil
}

// Rows is every row of a store, in seq order: what a test asserts the log holds.
func (j *Journal) Rows(store string) []todo.Change {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.select_(store, func(todo.Change) bool { return true })
}

func (j *Journal) select_(store string, keep func(todo.Change) bool) []todo.Change {
	var out []todo.Change
	for _, c := range j.rows {
		if c.Store == store && keep(c) {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b todo.Change) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}

func (j *Journal) apply(e todo.Edit) {
	if j.snap[e.Store] == nil {
		j.snap[e.Store] = map[string]todo.SnapshotRow{}
	}
	for _, p := range e.Delete {
		delete(j.snap[e.Store], p)
	}
	for _, r := range e.Put {
		j.snap[e.Store][r.Path] = r
	}
	if e.Repath != nil {
		s := copySettings(j.settings)
		s.ApplyRepath(e.Store, *e.Repath)
		j.settings = s
	}
}

func copySettings(s todo.Settings) todo.Settings {
	return todo.Settings{Stores: maps.Clone(s.Stores), Roots: maps.Clone(s.Roots), Vars: maps.Clone(s.Vars)}
}

// Disk is a todo.Filesystems over one memfs: the whole disk, "/" its root. Paths are canonical as
// written; memfs has no symlinks.
type Disk struct {
	FS *memfs.FS

	mu    sync.Mutex
	fault func(op, name string) error
	reads map[string]int
}

// NewDisk is an empty disk.
func NewDisk() *Disk { return &Disk{FS: memfs.New(), reads: map[string]int{}} }

// SetFault installs a hook called before every operation through an opened directory, with the
// operation ("stat", "readdir", "read", "write", "create", "remove", "rename", "mkdir") and the
// absolute name; an error is the operation's result.
func (d *Disk) SetFault(fn func(op, name string) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fault = fn
}

// Reads counts the files read whose absolute names start with prefix.
func (d *Disk) Reads(prefix string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for name, c := range d.reads {
		if strings.HasPrefix(name, prefix) {
			n += c
		}
	}
	return n
}

// ResetReads forgets the files read so far.
func (d *Disk) ResetReads() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reads = map[string]int{}
}

func (d *Disk) check(op, name string) error {
	d.mu.Lock()
	f := d.fault
	if op == "read" {
		d.reads[name]++
	}
	d.mu.Unlock()
	if f != nil {
		return f(op, name)
	}
	return nil
}

func rel(abs string) string {
	if r := strings.TrimPrefix(path.Clean(abs), "/"); r != "" {
		return r
	}
	return "."
}

func (d *Disk) Open(ctx context.Context, dir string) (vfs.FS, string, error) {
	if !path.IsAbs(dir) {
		return nil, "", fmt.Errorf("todotest: %q is not absolute", dir)
	}
	fi, err := d.FS.Stat(ctx, rel(dir))
	if err != nil {
		return nil, "", err
	}
	if !fi.IsDir() {
		return nil, "", &fs.PathError{Op: "open", Path: dir, Err: errors.New("not a directory")}
	}
	return &sub{d: d, prefix: rel(dir)}, path.Clean(dir), nil
}

// MkdirAll, WriteFile and ReadFile are test setup, past every fault.
func (d *Disk) MkdirAll(abs string) {
	if err := d.FS.MkdirAll(context.Background(), rel(abs)); err != nil {
		panic(err)
	}
}

func (d *Disk) WriteFile(abs string, content []byte) {
	dir, _ := path.Split(abs)
	d.MkdirAll(dir)
	if _, err := d.FS.WriteFile(context.Background(), rel(abs), strings.NewReader(string(content))); err != nil {
		panic(err)
	}
}

func (d *Disk) ReadFile(abs string) ([]byte, error) {
	r, err := d.FS.Open(context.Background(), rel(abs), 0)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Version is a file's vfs version, or "" when it does not exist.
func (d *Disk) Version(abs string) vfs.Version {
	fi, err := d.FS.Stat(context.Background(), rel(abs))
	if err != nil {
		return ""
	}
	return fi.Version
}

// sub is the disk seen from one directory.
type sub struct {
	d      *Disk
	prefix string
}

var (
	_ vfs.ConditionalWriter = (*sub)(nil)
	_ vfs.ExclusiveCreator  = (*sub)(nil)
	_ vfs.NoReplaceRenamer  = (*sub)(nil)
)

func (s *sub) full(name string) string {
	switch {
	case s.prefix == ".":
		return name
	case name == ".":
		return s.prefix
	}
	return s.prefix + "/" + name
}

func (s *sub) abs(name string) string { return "/" + strings.TrimPrefix(s.full(name), ".") }

func (s *sub) local(fi vfs.FileInfo) vfs.FileInfo {
	switch {
	case s.prefix == ".":
	case fi.Path == s.prefix:
		fi.Path = "."
	default:
		fi.Path = strings.TrimPrefix(fi.Path, s.prefix+"/")
	}
	return fi
}

func (s *sub) Stat(ctx context.Context, name string) (vfs.FileInfo, error) {
	if err := vfs.CheckName("stat", name); err != nil {
		return vfs.FileInfo{}, err
	}
	if err := s.d.check("stat", s.abs(name)); err != nil {
		return vfs.FileInfo{}, err
	}
	fi, err := s.d.FS.Stat(ctx, s.full(name))
	return s.local(fi), err
}

func (s *sub) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	if err := vfs.CheckName("readdir", name); err != nil {
		return nil, err
	}
	if err := s.d.check("readdir", s.abs(name)); err != nil {
		return nil, err
	}
	out, err := s.d.FS.ReadDir(ctx, s.full(name))
	for i := range out {
		out[i] = s.local(out[i])
	}
	return out, err
}

func (s *sub) Open(ctx context.Context, name string, offset int64) (io.ReadCloser, error) {
	if err := vfs.CheckName("open", name); err != nil {
		return nil, err
	}
	if err := s.d.check("read", s.abs(name)); err != nil {
		return nil, err
	}
	return s.d.FS.Open(ctx, s.full(name), offset)
}

func (s *sub) WriteFile(ctx context.Context, name string, r io.Reader, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	if err := s.d.check("write", s.abs(name)); err != nil {
		return vfs.FileInfo{}, err
	}
	fi, err := s.d.FS.WriteFile(ctx, s.full(name), r, opts...)
	return s.local(fi), err
}

func (s *sub) WriteFileIf(ctx context.Context, name string, r io.Reader, want vfs.Version, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	if err := s.d.check("write", s.abs(name)); err != nil {
		return vfs.FileInfo{}, err
	}
	fi, err := s.d.FS.WriteFileIf(ctx, s.full(name), r, want, opts...)
	return s.local(fi), err
}

func (s *sub) CreateExclusive(ctx context.Context, name string, r io.Reader, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	if err := s.d.check("create", s.abs(name)); err != nil {
		return vfs.FileInfo{}, err
	}
	fi, err := s.d.FS.CreateExclusive(ctx, s.full(name), r, opts...)
	return s.local(fi), err
}

func (s *sub) MkdirAll(ctx context.Context, name string) error {
	if err := vfs.CheckName("mkdir", name); err != nil {
		return err
	}
	if err := s.d.check("mkdir", s.abs(name)); err != nil {
		return err
	}
	return s.d.FS.MkdirAll(ctx, s.full(name))
}

func (s *sub) Remove(ctx context.Context, name string) error {
	if err := s.d.check("remove", s.abs(name)); err != nil {
		return err
	}
	return s.d.FS.Remove(ctx, s.full(name))
}

func (s *sub) RemoveIf(ctx context.Context, name string, want vfs.Version) error {
	if err := s.d.check("remove", s.abs(name)); err != nil {
		return err
	}
	return s.d.FS.RemoveIf(ctx, s.full(name), want)
}

func (s *sub) RemoveAll(ctx context.Context, name string) error {
	if err := s.d.check("remove", s.abs(name)); err != nil {
		return err
	}
	return s.d.FS.RemoveAll(ctx, s.full(name))
}

func (s *sub) Rename(ctx context.Context, from, to string) error {
	if err := s.d.check("rename", s.abs(from)); err != nil {
		return err
	}
	return s.d.FS.Rename(ctx, s.full(from), s.full(to))
}

func (s *sub) RenameNoReplace(ctx context.Context, from, to string) error {
	if err := s.d.check("rename", s.abs(from)); err != nil {
		return err
	}
	return s.d.FS.RenameNoReplace(ctx, s.full(from), s.full(to))
}

func (s *sub) Close() error { return nil }
