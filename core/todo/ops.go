package todo

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/schema"
)

// Retention of the change log.
const (
	// KeepChanges is how long a committed change is kept once every consumer has acknowledged it.
	KeepChanges = 30 * 24 * time.Hour
	// ConsumerSilence is how long a consumer may go unseen before it is unregistered; when it
	// returns it gets a resync.
	ConsumerSilence = 90 * 24 * time.Hour
)

// Partition is one month of the archive and how many task files it holds, from its listing alone.
type Partition struct {
	Month string `json:"month"` // "2026/05"
	Files int    `json:"files"`
}

// reading opens an At's store for a read: resolved, recovered, locked. The caller unlocks.
func (m *Manager) reading(ctx context.Context, at At) (where, *store, error) {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return w, nil, err
	}
	return m.locked(ctx, w)
}

// writing is reading for a write: the store is writable and claimed.
func (m *Manager) writing(ctx context.Context, at At) (where, *store, error) {
	w, err := m.writable(ctx, at)
	if err != nil {
		return w, nil, err
	}
	return m.locked(ctx, w)
}

func (m *Manager) locked(ctx context.Context, w where) (where, *store, error) {
	st, err := m.open(ctx, w)
	if err != nil {
		return w, nil, err
	}
	st.mu.Lock()
	if err := m.ready(ctx, st); err != nil {
		st.mu.Unlock()
		return w, nil, err
	}
	return w, st, nil
}

// List lists an At's tasks and malformed files. With no buckets it reads the active buckets
// only, never archived/. "archived" lists the archive's months with their file counts, and
// "archived/YYYY/MM" reads that month, uncached.
func (m *Manager) List(ctx context.Context, at At, opts ListOpts) (Listing, error) {
	_, st, err := m.reading(ctx, at)
	if err != nil {
		return Listing{}, err
	}
	defer st.mu.Unlock()
	buckets := opts.Buckets
	if len(buckets) == 0 {
		buckets = append(slices.Clone(activeBuckets), "automated")
	}
	out := Listing{Tasks: []Task{}, Malformed: []Malformed{}}
	var active []file
	scanned := false
	for _, b := range buckets {
		var files []file
		switch {
		case slices.Contains(activeBuckets, b) || b == "automated":
			if !scanned {
				if active, err = m.scanActive(ctx, st); err != nil {
					return Listing{}, err
				}
				m.diff(ctx, st, active, activePath, true)
				scanned = true
			}
			for _, f := range active {
				if paths.Under(f.path, b) {
					files = append(files, f)
				}
			}
		case b == "archived":
			parts, err := st.partitions(ctx)
			if err != nil {
				return Listing{}, err
			}
			for _, p := range parts {
				entries, _, err := st.list(ctx, "archived/"+p)
				if err != nil {
					return Listing{}, err
				}
				out.Partitions = append(out.Partitions, Partition{Month: p, Files: len(entries)})
			}
		case partitionRe.MatchString(b):
			if files, err = m.readPartition(ctx, st, b); err != nil {
				return Listing{}, err
			}
			m.diff(ctx, st, files, func(p string) bool { return strings.HasPrefix(p, b+"/") }, false)
		default:
			return Listing{}, fmt.Errorf("%w: bucket %q (open, in-progress, deferred, completed, archived, archived/YYYY/MM)", ErrInvalid, b)
		}
		for _, f := range files {
			switch {
			case f.bad != "":
				out.Malformed = append(out.Malformed, Malformed{Path: f.path, Reason: f.bad})
			case opts.matches(f.task):
				out.Tasks = append(out.Tasks, f.task)
			}
		}
	}
	out.Cursor, err = m.j.Head(ctx, st.id)
	return out, err
}

var partitionRe = regexp.MustCompile(`^archived/[0-9]{4}/[0-9]{2}$`)

func (o ListOpts) matches(t Task) bool {
	return (o.Tag == "" || slices.Contains(t.Tags, o.Tag)) && (o.Assignee == "" || t.Assignee == o.Assignee) &&
		(o.Priority == "" || t.Priority == o.Priority) && (!o.HasErrors || len(t.Errors) > 0)
}

// Get reads one task, fresh, with its rev and the file's Markdown body.
func (m *Manager) Get(ctx context.Context, at At, id string) (Task, error) {
	_, st, err := m.reading(ctx, at)
	if err != nil {
		return Task{}, err
	}
	defer st.mu.Unlock()
	p, err := st.find(ctx, id)
	if err != nil {
		return Task{}, err
	}
	doc, err := st.docs.Read(ctx, p)
	if err != nil {
		return Task{}, err
	}
	t, err := decode(doc.Content)
	if err != nil {
		return Task{}, fmt.Errorf("%w: %s is malformed: %w", ErrInvalid, p, err)
	}
	_, body, _ := splitFrontmatter(string(doc.Content))
	t.Path, t.Rev, t.Body = p, rev(doc.Content), body
	return t, nil
}

// render encodes t and reads it back through its format: what is written is what decodes, and a
// value the format refuses is ErrInvalid before anything is written.
func render(t Task) (Task, []byte, error) {
	content := encode(t)
	back, err := decode(content)
	if err != nil {
		return Task{}, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	back.Path, back.Rev = t.Path, rev(content)
	return back, content, nil
}

// Add creates a task in an At's store. Creating with an assignee is an assignment: an open task
// starts in-progress (D1), and its change is an assign, delivered as todos.assign's is.
func (m *Manager) Add(ctx context.Context, at At, spec Spec) (Task, error) {
	w, st, err := m.writing(ctx, at)
	if err != nil {
		return Task{}, err
	}
	defer st.mu.Unlock()
	if spec.Title == "" {
		return Task{}, fmt.Errorf("%w: title is required", ErrInvalid)
	}
	ts := stamp(m.now())
	id := spec.ID
	if id == "" {
		id = makeID(ts, spec.Title)
	} else if !safeID(id) {
		return Task{}, fmt.Errorf("%w: id %q is not a safe file name (letters, digits, - and _)", ErrInvalid, id)
	}
	status := spec.Status
	if status == "" {
		status = "open"
	}
	if spec.Assignee != "" && status == "open" {
		status = "in-progress" // creating with an assignee is an assignment (D1)
	}
	if !validStatus(status) {
		return Task{}, fmt.Errorf("%w: status %q", ErrInvalid, status)
	}
	t := Task{Version: newest.version, ID: id, Status: status, Title: spec.Title, Description: spec.Description,
		Due: spec.Due, Priority: spec.Priority, Assignee: spec.Assignee, Tags: spec.Tags, ADR: slices.Clone(spec.ADR),
		Review: slices.Clone(spec.Review), Blocked: spec.Blocked, Created: ts, Updated: ts, StatusChanged: ts}
	switch status {
	case "completed":
		t.CompletedAt = ts
	case "archived":
		t.ArchivedAt = ts
	}
	sc, err := m.scope(ctx, w)
	if err != nil {
		return Task{}, err
	}
	sc, done := m.withExists(ctx, sc)
	sc.symbolizeRefs(&t)
	done()
	if _, err := st.find(ctx, id); err == nil {
		return Task{}, fmt.Errorf("%w: task %s", ErrExists, id)
	} else if !errors.Is(err, ErrNotFound) {
		return Task{}, err
	}
	t.Path = pathOf(t)
	back, content, err := render(t)
	if err != nil {
		return Task{}, err
	}
	c := Change{ID: id, Kind: KindAdd, To: status}
	if spec.Assignee != "" {
		c = Change{ID: id, Kind: KindAssign, To: spec.Assignee} // delivered like todos.assign
	}
	return m.create(ctx, st, c, back, content)
}

// Patch changes hand-editable fields by name: title, description, priority, due, tags, adr,
// review, blocked. A nil, "" or empty list clears an optional field. A field the format manages,
// status and assignee are refused: they change through the rules and their own verbs.
type Patch map[string]any

// Update applies a patch to a task the client saw at rev.
func (m *Manager) Update(ctx context.Context, at At, id, seen string, patch Patch) (Task, error) {
	w, st, err := m.writing(ctx, at)
	if err != nil {
		return Task{}, err
	}
	defer st.mu.Unlock()
	t, ver, err := st.current(ctx, id, seen)
	if err != nil {
		return Task{}, err
	}
	before := clone(t)
	f := formats[t.Version]
	for _, k := range slices.Sorted(maps.Keys(patch)) {
		decl, ok := f.schema.Field(k)
		switch {
		case !ok:
			return Task{}, fmt.Errorf("%w: %q is not a field of format v%d", ErrInvalid, k, f.version)
		case decl.Managed:
			return Task{}, fmt.Errorf("%w: %s is managed: only the daemon writes it", ErrInvalid, k)
		case k == "status":
			return Task{}, fmt.Errorf("%w: status changes through todos.status", ErrInvalid)
		case k == "assignee":
			return Task{}, fmt.Errorf("%w: assignee changes through todos.assign", ErrInvalid)
		}
		v, err := patchValue(decl, patch[k])
		if err != nil {
			return Task{}, err
		}
		if k == "title" && v == "" {
			return Task{}, fmt.Errorf("%w: title cannot be empty", ErrInvalid)
		}
		t.set(k, v)
	}
	sc, err := m.scope(ctx, w)
	if err != nil {
		return Task{}, err
	}
	sc, done := m.withExists(ctx, sc)
	sc.symbolizeRefs(&t)
	done()
	t.Updated = stamp(m.now())
	back, content, err := render(t)
	if err != nil {
		return Task{}, err
	}
	c := Change{ID: id, Kind: KindUpdate, FromRev: seen, Fields: changedFields(t, digests(before), digests(back))}
	return m.rewrite(ctx, st, c, back, ver, content)
}

// patchValue is a patch value as its field's Go value: a string, or a list of strings.
func patchValue(decl schema.Field, v any) (any, error) {
	bad := fmt.Errorf("%w: %s takes %s", ErrInvalid, decl.Name, decl.Type)
	if decl.Type == schema.List {
		switch x := v.(type) {
		case nil:
			return []string(nil), nil
		case string:
			if x == "" {
				return []string(nil), nil
			}
			return []string{x}, nil
		case []string:
			return slices.Clone(x), nil
		case []any:
			out := make([]string, 0, len(x))
			for _, it := range x {
				s, ok := it.(string)
				if !ok {
					return nil, bad
				}
				out = append(out, s)
			}
			return out, nil
		}
		return nil, bad
	}
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	}
	return nil, bad
}

// Status moves a task to another status, with auto-core's timestamps, into its new bucket. The
// same status is a no-op.
func (m *Manager) Status(ctx context.Context, at At, id, seen, status string) (Task, error) {
	if !validStatus(status) {
		return Task{}, fmt.Errorf("%w: status %q (open, in-progress, deferred, completed, archived)", ErrInvalid, status)
	}
	_, st, err := m.writing(ctx, at)
	if err != nil {
		return Task{}, err
	}
	defer st.mu.Unlock()
	t, ver, err := st.current(ctx, id, seen)
	if err != nil || t.Status == status {
		return t, err
	}
	old, from := t.Status, t.Path
	setStatus(&t, status, stamp(m.now()))
	t.Path = pathOf(t)
	back, content, err := render(t)
	if err != nil {
		return Task{}, err
	}
	return m.move(ctx, st, Change{ID: id, Kind: KindStatus, From: old, To: status, FromRev: seen}, from, ver, back, content)
}

// Assign sets or clears a task's assignee. Assigning an open task moves it to in-progress in the
// same write (D1); nothing else changes status. Assigning again, even to the same assignee, is a
// new assignment: it is written and logged, so it is delivered again. Clearing an unassigned task
// is a no-op.
func (m *Manager) Assign(ctx context.Context, at At, id, seen, assignee, reason string) (Task, error) {
	_, st, err := m.writing(ctx, at)
	if err != nil {
		return Task{}, err
	}
	defer st.mu.Unlock()
	t, ver, err := st.current(ctx, id, seen)
	if err != nil {
		return Task{}, err
	}
	old, from := t.Assignee, t.Path
	if assignee == "" && old == "" {
		return t, nil
	}
	ts := stamp(m.now())
	t.set("assignee", assignee)
	t.Updated = ts
	if assignee != "" && t.Status == "open" {
		setStatus(&t, "in-progress", ts)
	}
	t.Path = pathOf(t)
	back, content, err := render(t)
	if err != nil {
		return Task{}, err
	}
	c := Change{ID: id, Kind: KindAssign, From: old, To: assignee, Reason: reason, FromRev: seen}
	if back.Path != from {
		return m.move(ctx, st, c, from, ver, back, content)
	}
	return m.rewrite(ctx, st, c, back, ver, content)
}

// Remove deletes a task the client saw at rev.
func (m *Manager) Remove(ctx context.Context, at At, id, seen string) error {
	_, st, err := m.writing(ctx, at)
	if err != nil {
		return err
	}
	defer st.mu.Unlock()
	t, ver, err := st.current(ctx, id, seen)
	if err != nil {
		return err
	}
	return m.remove(ctx, st, Change{ID: id, Kind: KindRemove, From: t.Status, FromRev: seen}, t.Path, ver)
}

// RemoveMalformed deletes one malformed file, by its store-relative path. A valid task is removed
// with Remove; a file using removed automation is never deleted by the daemon.
func (m *Manager) RemoveMalformed(ctx context.Context, at At, p string) error {
	_, st, err := m.writing(ctx, at)
	if err != nil {
		return err
	}
	defer st.mu.Unlock()
	if p != path.Clean(p) || !eligible(p) || p == ownerMarker || strings.HasPrefix(p, trashDir+"/") {
		return fmt.Errorf("%w: %q is not a task file of the store", ErrInvalid, p)
	}
	if strings.HasPrefix(p, "automated/") {
		return fmt.Errorf("%w: %s: %s; the daemon never deletes it", ErrInvalid, p, automationRemoved)
	}
	doc, err := st.docs.Read(ctx, p)
	if errors.Is(err, vfs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, p)
	}
	if err != nil {
		return err
	}
	_, derr := decode(doc.Content)
	switch {
	case derr == nil:
		return fmt.Errorf("%w: %s is a valid task: todos.remove removes it", ErrInvalid, p)
	case strings.Contains(derr.Error(), automationRemoved):
		return fmt.Errorf("%w: %s: %w; the daemon never deletes it", ErrInvalid, p, derr)
	}
	c := Change{ID: strings.TrimSuffix(path.Base(p), ".md"), Kind: KindRemoveMalformed, Reason: derr.Error(), FromRev: rev(doc.Content)}
	return m.remove(ctx, st, c, p, doc.Version)
}

// Refresh reconciles the active buckets as auto-core's refresh does: refs symbolized, a completed
// task over 28 days archived, errors[] recomputed, a misplaced file moved, and a duplicate left by
// an interrupted move removed. Archives are not read: a refresh never rewrites them. A refresh
// that finds nothing to change writes nothing.
func (m *Manager) Refresh(ctx context.Context, at At) (RefreshSummary, error) {
	w, st, err := m.writing(ctx, at)
	if err != nil {
		return RefreshSummary{}, err
	}
	defer st.mu.Unlock()
	files, err := m.scanActive(ctx, st)
	if err != nil {
		return RefreshSummary{}, err
	}
	m.diff(ctx, st, files, activePath, true)
	var sum RefreshSummary
	sum.Scanned = len(files)
	files = m.dedupe(ctx, st, files, &sum)
	sc, err := m.scope(ctx, w)
	if err != nil {
		return sum, err
	}
	sc, done := m.withExists(ctx, sc)
	defer done()
	ts := stamp(m.now())
	has := func(id string) bool { _, err := st.find(ctx, id); return err == nil }
	for _, f := range files {
		if f.bad != "" {
			sum.Malformed++
			continue
		}
		t := clone(f.task)
		changed := sc.symbolizeRefs(&t)
		kind := KindRefresh
		if t.Status == "completed" && t.CompletedAt != "" && agedOut(t.CompletedAt, ts) {
			setStatus(&t, "archived", ts)
			kind, changed = KindArchive, true
			sum.Archived++
		}
		if errs := sc.refErrors(t, ts, has); !sameErrors(errs, t.Errors) {
			t.Errors, changed = errs, true
			if len(errs) > 0 {
				sum.ErrorsSet++
			}
		}
		t.Path = pathOf(t)
		if !changed && t.Path == f.path {
			continue
		}
		back, content, err := render(t)
		if err != nil {
			logger.Warning(m.o.log, err, logger.Fields{"event": "todos.refresh.render", "store": st.id, "path": f.path})
			continue
		}
		c := Change{ID: t.ID, Kind: kind, From: f.task.Status, To: back.Status, FromRev: f.task.Rev,
			Fields: changedFields(back, digests(f.task), digests(back))}
		ver := st.cache[f.path].version
		if back.Path != f.path {
			_, err = m.move(ctx, st, c, f.path, ver, back, content)
			if err == nil {
				sum.Moved++
			}
		} else if _, err = m.rewrite(ctx, st, c, back, ver, content); err == nil {
			sum.Rewritten++
		}
		if err != nil {
			logger.Warning(m.o.log, err, logger.Fields{"event": "todos.refresh.write", "store": st.id, "path": f.path})
		}
	}
	m.prune(ctx, st)
	return sum, nil
}

// dedupe settles a task id found at two active paths. When the two differ only in what a status
// change writes, the copy with the older status_changed is what an interrupted move left behind,
// and it is removed; otherwise the later copy is skipped, and counted malformed.
func (m *Manager) dedupe(ctx context.Context, st *store, files []file, sum *RefreshSummary) []file {
	first := map[string]int{}
	skip := map[int]bool{}
	for i, f := range files {
		if f.bad != "" {
			continue
		}
		j, dup := first[f.task.ID]
		if !dup {
			first[f.task.ID] = i
			continue
		}
		a, b := files[j].task, f.task
		older, keep := j, i
		if b.StatusChanged < a.StatusChanged {
			older, keep = i, j
		}
		if !sameTask(a, b) || a.StatusChanged == b.StatusChanged {
			skip[i] = true
			sum.Malformed++
			continue
		}
		o := files[older]
		c := Change{ID: o.task.ID, Kind: KindRemove, From: o.task.Status, FromRev: o.task.Rev,
			Reason: "a duplicate an interrupted move left; the task is at " + files[keep].path}
		if err := m.remove(ctx, st, c, o.path, st.cache[o.path].version); err != nil {
			logger.Warning(m.o.log, err, logger.Fields{"event": "todos.refresh.duplicate", "store": st.id, "path": o.path})
			skip[i] = true
			continue
		}
		skip[older] = true
		first[f.task.ID] = keep
	}
	var out []file
	for i, f := range files {
		if !skip[i] {
			out = append(out, f)
		}
	}
	return out
}

// sameTask reports two copies of a task equal but for what a status change writes.
func sameTask(a, b Task) bool {
	strip := func(t Task) Task {
		t.Status, t.StatusChanged, t.CompletedAt, t.ArchivedAt, t.Updated = "", "", "", "", ""
		t.Errors, t.Path, t.Rev, t.blank = nil, "", "", nil
		return t
	}
	return reflect.DeepEqual(strip(a), strip(b))
}

// prune unregisters consumers silent past ConsumerSilence and drops committed changes older than
// KeepChanges that every registered consumer has acknowledged.
func (m *Manager) prune(ctx context.Context, st *store) {
	now := m.now()
	cs, err := m.j.Consumers(ctx, st.id)
	if err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.prune.failed", "store": st.id})
		return
	}
	upTo, err := m.j.Head(ctx, st.id)
	if err != nil {
		return
	}
	for _, c := range cs {
		if c.resyncing(now) {
			// what follows its resync's cursor is kept for it: its ack resumes there
			upTo = min(upTo, c.Resync)
		}
		if c.Unregistered {
			continue
		}
		if now.Sub(c.SeenAt) > ConsumerSilence {
			c.Unregistered = true
			if err := m.j.PutConsumer(ctx, st.id, c); err == nil {
				m.event(Event{Kind: "todos.consumer_dropped", Store: st.id, Detail: c.Name})
			}
			continue
		}
		upTo = min(upTo, c.Acked)
	}
	if _, err := m.j.Prune(ctx, st.id, now.Add(-KeepChanges), upTo); err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.prune.failed", "store": st.id})
	}
}

// Changes is a consumer's next committed changes after what it acknowledged. A consumer seen for
// the first time starts at the beginning of the retained log; one unregistered for its silence
// gets a resync: the store's current snapshot, and a cursor at the head. A resync is delivered at
// least once, as a page of changes is: the consumer stays unregistered, and is answered a resync
// again, until it acknowledges the resync's cursor.
func (m *Manager) Changes(ctx context.Context, at At, consumer string, limit int) (ChangesPage, error) {
	if consumer == "" {
		return ChangesPage{}, fmt.Errorf("%w: a consumer name is required", ErrInvalid)
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	w, err := m.resolve(ctx, at)
	if err != nil {
		return ChangesPage{}, err
	}
	id, now := w.rec.ID, m.now()
	head, err := m.j.Head(ctx, id)
	if err != nil {
		return ChangesPage{}, err
	}
	c, found, err := m.consumer(ctx, id, consumer)
	if err != nil {
		return ChangesPage{}, err
	}
	if found && (c.Unregistered || now.Sub(c.SeenAt) > ConsumerSilence) {
		snap, err := m.j.Snapshot(ctx, id)
		if err != nil {
			return ChangesPage{}, err
		}
		if !c.Unregistered { // silent too long, and no prune has dropped it yet
			m.event(Event{Kind: "todos.consumer_dropped", Store: id, Detail: consumer})
		}
		// Acked stays: the cursor moves only when the client acknowledges this page, after it has
		// durably handled it (ADR 1791532020 §2.5). A client that dies first is answered it again.
		c.Unregistered, c.SeenAt, c.Resync = true, now, head
		if err := m.j.PutConsumer(ctx, id, c); err != nil {
			return ChangesPage{}, err
		}
		return ChangesPage{Resync: true, Snapshot: snap, Cursor: head}, nil
	}
	if !found {
		c = Consumer{Name: consumer}
	}
	rows, err := m.j.After(ctx, id, c.Acked, limit)
	if err != nil {
		return ChangesPage{}, err
	}
	c.SeenAt = now
	if err := m.j.PutConsumer(ctx, id, c); err != nil {
		return ChangesPage{}, err
	}
	return ChangesPage{Changes: rows, Cursor: head}, nil
}

// Ack advances a consumer's cursor to seq, once it has durably handled every change up to it. A
// cursor never moves back. An unregistered consumer is registered again by acknowledging the
// resync it was answered, at its cursor or later; any other ack of it is from before its silence,
// for changes that may be pruned since, and is refused.
func (m *Manager) Ack(ctx context.Context, at At, consumer string, seq int64) error {
	w, err := m.resolve(ctx, at)
	if err != nil {
		return err
	}
	c, found, err := m.consumer(ctx, w.rec.ID, consumer)
	if err != nil {
		return err
	}
	now := m.now()
	if !found {
		return fmt.Errorf("%w: consumer %q (todos.changes registers it)", ErrNotFound, consumer)
	}
	if c.Unregistered && (!c.resyncing(now) || seq < c.Resync) {
		return fmt.Errorf("%w: consumer %q was unregistered for its silence: todos.changes answers a resync, and acknowledging its cursor registers it again",
			ErrNotFound, consumer)
	}
	head, err := m.j.Head(ctx, w.rec.ID)
	if err != nil {
		return err
	}
	if seq > head {
		return fmt.Errorf("%w: seq %d is past the log's head %d", ErrInvalid, seq, head)
	}
	c.Acked, c.SeenAt, c.Unregistered, c.Resync = max(c.Acked, seq), now, false, 0
	return m.j.PutConsumer(ctx, w.rec.ID, c)
}

func (m *Manager) consumer(ctx context.Context, store, name string) (Consumer, bool, error) {
	cs, err := m.j.Consumers(ctx, store)
	if err != nil {
		return Consumer{}, false, err
	}
	for _, c := range cs {
		if c.Name == name {
			return c, true, nil
		}
	}
	return Consumer{}, false, nil
}
