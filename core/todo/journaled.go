package todo

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
)

// Every file change is journaled: a pending row before the first file step, committed with its
// snapshot edit after the last. A step that fails settles its row at once, by the same rules
// recovery applies after a crash, so the row exists if and only if the file change happened.

// begin inserts c as a pending row.
func (m *Manager) begin(ctx context.Context, st *store, c Change) (Change, error) {
	c.Store, c.State, c.At = st.id, StatePending, m.now()
	seq, err := m.j.Begin(ctx, c)
	c.Seq = seq
	return c, err
}

// commit marks c committed with its snapshot edit.
func (m *Manager) commit(ctx context.Context, st *store, c Change, e Edit) error {
	e.Store = st.id
	return m.j.Commit(ctx, c.Seq, e)
}

// failed settles c after a failed file step and returns the step's error.
func (m *Manager) failed(ctx context.Context, st *store, c Change, err error) error {
	if rerr := m.recover(ctx, st, c); rerr != nil {
		logger.Warning(m.o.log, rerr, logger.Fields{"event": "todos.settle.failed", "store": st.id, "seq": c.Seq})
	}
	return err
}

// settle is a failed first write: a condition that failed changed nothing, so the row goes;
// anything else is settled by what is on disk.
func (m *Manager) settle(ctx context.Context, st *store, c Change, err, as error, p string) error {
	if errors.Is(err, vfs.ErrConflict) {
		return m.dropped(ctx, c, fmt.Errorf("%w: %s", as, p))
	}
	return m.failed(ctx, st, c, err)
}

// dropped deletes the row of a change that made no file change, and returns why.
func (m *Manager) dropped(ctx context.Context, c Change, err error) error {
	if derr := m.j.Drop(ctx, c.Seq); derr != nil {
		logger.Warning(m.o.log, derr, logger.Fields{"event": "todos.settle.failed", "store": c.Store, "seq": c.Seq})
	}
	return err
}

// create writes a new task file that must not exist yet.
func (m *Manager) create(ctx context.Context, st *store, c Change, t Task, content []byte) (Task, error) {
	c.Op, c.ToPath, c.ToRev = OpCreate, t.Path, rev(content)
	c, err := m.begin(ctx, st, c)
	if err != nil {
		return Task{}, err
	}
	if _, err := st.docs.Write(ctx, t.Path, content, ""); err != nil {
		return Task{}, m.settle(ctx, st, c, err, ErrExists, t.Path)
	}
	t.Rev = c.ToRev
	return t, m.commit(ctx, st, c, Edit{Put: []SnapshotRow{rowOf(t)}})
}

// rewrite replaces a task file in place, if it is still at the version it was read at.
func (m *Manager) rewrite(ctx context.Context, st *store, c Change, t Task, want vfs.Version, content []byte) (Task, error) {
	c.Op, c.FromPath, c.ToPath, c.ToRev = OpInPlace, t.Path, t.Path, rev(content)
	c, err := m.begin(ctx, st, c)
	if err != nil {
		return Task{}, err
	}
	if _, err := st.docs.Write(ctx, t.Path, content, want); err != nil {
		return Task{}, m.settle(ctx, st, c, err, ErrStale, t.Path)
	}
	t.Rev = c.ToRev
	return t, m.commit(ctx, st, c, Edit{Put: []SnapshotRow{rowOf(t)}})
}

// move writes a task's new file, then removes the old one if it is still at the version it was
// read at.
func (m *Manager) move(ctx context.Context, st *store, c Change, from string, want vfs.Version, t Task, content []byte) (Task, error) {
	c.Op, c.FromPath, c.ToPath, c.ToRev = OpMove, from, t.Path, rev(content)
	c, err := m.begin(ctx, st, c)
	if err != nil {
		return Task{}, err
	}
	if _, err := st.docs.Write(ctx, t.Path, content, ""); err != nil {
		return Task{}, m.settle(ctx, st, c, err, ErrExists, t.Path)
	}
	if err := st.docs.Remove(ctx, from, want); err != nil {
		return Task{}, m.failed(ctx, st, c, conflictAs(err, ErrStale, from))
	}
	t.Rev = c.ToRev
	return t, m.commit(ctx, st, c, Edit{Put: []SnapshotRow{rowOf(t)}, Delete: []string{from}})
}

// trashPath is where a delete puts its file before removing it: deterministic, so recovery can
// tell the daemon's own half-done delete from a file someone else removed.
func trashPath(c Change) string {
	return trashDir + "/" + strconv.FormatInt(c.Seq, 10) + "-" + path.Base(c.FromPath)
}

// remove deletes a file in two marked steps: into the store's trash, staged, then gone.
func (m *Manager) remove(ctx context.Context, st *store, c Change, p string, want vfs.Version) error {
	c.Op, c.FromPath = OpDelete, p
	c, err := m.begin(ctx, st, c)
	if err != nil {
		return err
	}
	if err := st.fsys.MkdirAll(ctx, trashDir); err != nil {
		return m.failed(ctx, st, c, err)
	}
	// core/docs renames without a version condition; the stat narrows the window, and the rev
	// read back below closes it
	if fi, err := st.fsys.Stat(ctx, p); err != nil || fi.Version != want {
		return m.dropped(ctx, c, fmt.Errorf("%w: %s", ErrStale, p))
	}
	trash := trashPath(c)
	if err := st.docs.Rename(ctx, p, trash); err != nil {
		return m.failed(ctx, st, c, err)
	}
	doc, err := st.docs.Read(ctx, trash)
	if err != nil {
		return m.failed(ctx, st, c, err)
	}
	if rev(doc.Content) != c.FromRev { // edited in the window: put it back untouched
		if err := st.docs.Rename(ctx, trash, p); err != nil {
			return m.failed(ctx, st, c, err)
		}
		return m.dropped(ctx, c, fmt.Errorf("%w: %s", ErrStale, p))
	}
	if err := m.j.Stage(ctx, c.Seq); err != nil {
		return err // a crash here: recovery finds the file in the trash with the source rev
	}
	c.State = StateStaged
	if err := st.docs.Remove(ctx, trash, doc.Version); err != nil {
		return m.failed(ctx, st, c, err)
	}
	return m.commit(ctx, st, c, Edit{Delete: []string{p}})
}

// conflictAs maps a vfs condition that failed to the todo error a client acts on.
func conflictAs(err, as error, p string) error {
	if errors.Is(err, vfs.ErrConflict) {
		return fmt.Errorf("%w: %s", as, p)
	}
	return err
}

// state is a file as recovery finds it: absent, or present with a rev.
type state struct {
	exists bool
	rev    string
	ver    vfs.Version
}

func (st *store) state(ctx context.Context, p string) (state, error) {
	doc, err := st.docs.Read(ctx, p)
	if errors.Is(err, vfs.ErrNotExist) {
		return state{}, nil
	}
	if err != nil {
		return state{}, err
	}
	return state{exists: true, rev: rev(doc.Content), ver: doc.Version}, nil
}

// recover settles one pending or staged row by what is on disk: commit when
// the change happened, drop the row when nothing did, finish an interrupted move or delete, and
// call anything else ambiguous: the row goes, the ambiguity is reported, and the next snapshot
// diff logs the disk as it is.
func (m *Manager) recover(ctx context.Context, st *store, c Change) error {
	src, err := st.stateIf(ctx, c.FromPath)
	if err != nil {
		return err
	}
	dst, err := st.stateIf(ctx, c.ToPath)
	if err != nil {
		return err
	}
	commit := func(e Edit) error { return m.commit(ctx, st, c, e) }
	put := func() Edit {
		if f, _ := st.load(ctx, c.ToPath); f.bad == "" {
			return Edit{Put: []SnapshotRow{rowOf(f.task)}}
		}
		return Edit{}
	}
	drop := func() error { return m.j.Drop(ctx, c.Seq) }
	switch c.Op {
	case OpCreate:
		switch {
		case dst.exists && dst.rev == c.ToRev:
			return commit(put())
		case !dst.exists:
			return drop()
		}
	case OpInPlace:
		switch {
		case dst.exists && dst.rev == c.ToRev:
			return commit(put())
		case dst.exists && dst.rev == c.FromRev:
			return drop()
		}
	case OpMove:
		switch {
		case !dst.exists && src.exists && src.rev == c.FromRev:
			return drop()
		case dst.exists && dst.rev == c.ToRev && src.exists && src.rev == c.FromRev:
			if err := st.docs.Remove(ctx, c.FromPath, src.ver); err != nil {
				return err
			}
			e := put()
			e.Delete = []string{c.FromPath}
			return commit(e)
		case dst.exists && dst.rev == c.ToRev && !src.exists:
			e := put()
			e.Delete = []string{c.FromPath}
			return commit(e)
		}
	case OpDelete:
		trash, err := st.state(ctx, trashPath(c))
		if err != nil {
			return err
		}
		gone := Edit{Delete: []string{c.FromPath}}
		switch {
		case c.State == StateStaged && trash.exists:
			if err := st.docs.Remove(ctx, trashPath(c), trash.ver); err != nil {
				return err
			}
			return commit(gone)
		case c.State == StateStaged:
			return commit(gone)
		case src.exists && src.rev == c.FromRev && !trash.exists:
			return drop()
		case !src.exists && trash.exists && trash.rev == c.FromRev: // moved to the trash, not yet staged
			if err := st.docs.Remove(ctx, trashPath(c), trash.ver); err != nil {
				return err
			}
			return commit(gone)
		}
	}
	return m.ambiguous(ctx, st, c)
}

// stateIf is state for a path a row may leave empty.
func (st *store) stateIf(ctx context.Context, p string) (state, error) {
	if p == "" {
		return state{}, nil
	}
	return st.state(ctx, p)
}

// ambiguous drops a row whose file changed or vanished outside the daemon, and reports it.
func (m *Manager) ambiguous(ctx context.Context, st *store, c Change) error {
	m.event(Event{Kind: "todos.ambiguous", Store: st.id, ID: c.ID, Path: firstNonEmpty(c.ToPath, c.FromPath),
		Detail: c.Kind + " (" + c.Op + "): the file changed outside the daemon while the change was pending"})
	logger.Warning(m.o.log, nil, logger.Fields{"event": "todos.recover.ambiguous", "store": st.id, "id": c.ID, "kind": c.Kind, "seq": c.Seq})
	return m.j.Drop(ctx, c.Seq)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// recoverMoves settles an interrupted store move before the store is opened: the new path there
// and the old gone is a move that happened, the reverse one that did not, and anything else leaves
// the store read-only until a person decides.
func (m *Manager) recoverMoves(ctx context.Context, id string, again bool) error {
	m.mu.Lock()
	done := m.movesSettled[id]
	m.mu.Unlock()
	if done && !again {
		return nil
	}
	rows, err := m.j.Pending(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if c.Op != OpStoreMove {
			continue
		}
		oldThere, err := m.dirExists(ctx, c.FromPath)
		if err != nil {
			return err
		}
		newThere, err := m.dirExists(ctx, c.ToPath)
		if err != nil {
			return err
		}
		switch {
		case newThere && !oldThere:
			if err := m.j.Commit(ctx, c.Seq, Edit{Store: id, Repath: &Repath{From: c.FromPath, To: c.ToPath}}); err != nil {
				return err
			}
		case oldThere && !newThere:
			if err := m.j.Drop(ctx, c.Seq); err != nil {
				return err
			}
		default:
			why := "an interrupted store move from " + c.FromPath + " to " + c.ToPath + " left "
			if newThere {
				why += "both directories"
			} else {
				why += "neither directory"
			}
			m.mu.Lock()
			m.blocked[id] = why
			m.mu.Unlock()
			m.event(Event{Kind: "todos.store_move_blocked", Store: id, Path: c.FromPath, Detail: why})
		}
	}
	m.mu.Lock()
	m.movesSettled[id] = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) dirExists(ctx context.Context, dir string) (bool, error) {
	fsys, _, err := m.disk.Open(ctx, dir)
	if errors.Is(err, vfs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fsys.Close()
	return true, nil
}

// MoveStore moves an At's store directory to an absolute path where nothing is yet, and points
// every root that resolved to it there. The store keeps its id, so its claim, snapshot and
// consumers' cursors move with it.
func (m *Manager) MoveStore(ctx context.Context, at At, to string) (Resolved, error) {
	w, err := m.writable(ctx, at)
	if err != nil {
		return Resolved{}, err
	}
	if !path.IsAbs(to) || path.Clean(to) != to || to == "/" {
		return Resolved{}, fmt.Errorf("%w: %q is not a clean absolute path", ErrInvalid, to)
	}
	st, err := m.open(ctx, w)
	if err != nil {
		return Resolved{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := m.ready(ctx, st); err != nil {
		return Resolved{}, err
	}
	parent, base := path.Split(to)
	pfs, canonParent, err := m.disk.Open(ctx, path.Clean(parent))
	if err != nil {
		return Resolved{}, fmt.Errorf("%w: %s's parent must be an existing directory: %w", ErrInvalid, to, err)
	}
	pfs.Close()
	target := path.Join(canonParent, base)
	if target == st.path || strings.HasPrefix(target, st.path+"/") {
		return Resolved{}, fmt.Errorf("%w: %s is inside the store", ErrInvalid, target)
	}
	if ok, err := m.dirExists(ctx, target); err != nil || ok {
		if err == nil {
			err = fmt.Errorf("%w: %s", ErrExists, target)
		}
		return Resolved{}, err
	}
	ancestor := commonDir(st.path, target)
	afs, _, err := m.disk.Open(ctx, ancestor)
	if err != nil {
		return Resolved{}, err
	}
	defer afs.Close()
	from, dest := rel(ancestor, st.path), rel(ancestor, target)
	d := docs.New(afs, func(p string) bool { return p == from || p == dest })
	c, err := m.begin(ctx, st, Change{Kind: KindMoveStore, Op: OpStoreMove, From: st.path, To: target, FromPath: st.path, ToPath: target})
	if err != nil {
		return Resolved{}, err
	}
	if err := d.Rename(ctx, from, dest); err != nil {
		if rerr := m.recoverMoves(ctx, st.id, true); rerr != nil {
			logger.Warning(m.o.log, rerr, logger.Fields{"event": "todos.settle.failed", "store": st.id, "seq": c.Seq})
		}
		return Resolved{}, conflictAs(err, ErrExists, target)
	}
	if err := m.j.Commit(ctx, c.Seq, Edit{Store: st.id, Repath: &Repath{From: st.path, To: target}}); err != nil {
		return Resolved{}, err // the next open finishes it: the new path is there, the old one gone
	}
	m.mu.Lock()
	st.fsys.Close()
	delete(m.stores, st.id)
	m.mu.Unlock()
	return m.Resolve(ctx, at)
}

// commonDir is the deepest directory holding both a and b.
func commonDir(a, b string) string {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	n := 0
	for n < len(as)-1 && n < len(bs)-1 && as[n] == bs[n] {
		n++
	}
	if d := strings.Join(as[:n], "/"); d != "" {
		return d
	}
	return "/"
}

// rel is p relative to dir, which holds it.
func rel(dir, p string) string {
	if dir == "/" {
		return strings.TrimPrefix(p, "/")
	}
	return strings.TrimPrefix(p, dir+"/")
}
