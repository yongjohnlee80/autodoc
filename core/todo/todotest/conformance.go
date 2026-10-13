package todotest

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
)

// Harness is a todo.Journal implementation, for RunJournalConformance.
type Harness struct {
	// New is an empty journal.
	New func(t *testing.T) todo.Journal
	// Reopen is what a restarted daemon finds of j: the journal closed and opened again.
	Reopen func(t *testing.T, j todo.Journal) todo.Journal
	// Break makes j's transactions that save a root record fail, as late as the implementation can
	// make them: after the row, the snapshot and the store records are written. The in-memory
	// journal fails its Commit, Record and UpdateSettings before they start; either way nothing of
	// them may stay. undo ends it.
	Break func(t *testing.T, j todo.Journal) (undo func())
}

// RunJournalConformance is the behaviour every todo.Journal shares: what core/todo relies on of
// the in-memory journal and of core/store's SQLite one alike. A time is compared as an instant (an
// implementation need not keep its zone), and an empty list or map as none.
func RunJournalConformance(t *testing.T, h Harness) {
	t.Run("the change log", func(t *testing.T) { conformLog(t, h) })
	t.Run("record", func(t *testing.T) { conformRecord(t, h) })
	t.Run("after pages", func(t *testing.T) { conformAfter(t, h) })
	t.Run("prune", func(t *testing.T) { conformPrune(t, h) })
	t.Run("snapshot", func(t *testing.T) { conformSnapshot(t, h) })
	t.Run("consumers", func(t *testing.T) { conformConsumers(t, h) })
	t.Run("settings", func(t *testing.T) { conformSettings(t, h) })
	t.Run("repath", func(t *testing.T) { conformRepath(t, h) })
	t.Run("a failed transaction leaves nothing", func(t *testing.T) { conformAtomic(t, h) })
	t.Run("reopen", func(t *testing.T) { conformReopen(t, h) })
}

var (
	cctx  = context.Background()
	t0    = time.Date(2026, 10, 1, 9, 0, 0, 123456789, time.UTC)
	pdt   = time.FixedZone("PDT", -7*3600)
	errFn = errors.New("todotest: fn refused")
)

// full is a change with every field set; Seq and State are the journal's to give.
func full(store, id string) todo.Change {
	return todo.Change{Seq: 77, Store: store, ID: id, Kind: todo.KindStatus, Op: todo.OpMove, From: "open", To: "completed",
		Fields: []string{"status", "title"}, Reason: "done", Hand: true, FromPath: "open/" + id + ".md",
		ToPath: "completed/" + id + ".md", FromRev: "r1", ToRev: "r2", State: todo.StateCommitted, At: t0.In(pdt)}
}

// as is c as the journal holds it at seq, in state.
func as(c todo.Change, seq int64, state string) todo.Change {
	c.Seq, c.State = seq, state
	return c
}

func conformLog(t *testing.T, h Harness) {
	j := h.New(t)
	a1, b2, a3 := full("A", "t1"), full("B", "t2"), full("A", "t3")
	a3.Fields, a3.Hand = nil, false
	s1, s2, s3 := begin(t, j, a1), begin(t, j, b2), begin(t, j, a3)
	same(t, "seqs rise across stores", s1 < s2 && s2 < s3, true)
	same(t, "pending A", pending(t, j, "A"), []todo.Change{as(a1, s1, todo.StatePending), as(a3, s3, todo.StatePending)})

	must(t, j.Stage(cctx, s3))
	fails(t, "staging a staged row", j.Stage(cctx, s3))
	fails(t, "staging no row", j.Stage(cctx, 9999))
	same(t, "a staged row", pending(t, j, "A")[1].State, todo.StateStaged)

	must(t, j.Commit(cctx, s3, todo.Edit{}))
	must(t, j.Commit(cctx, s1, todo.Edit{}))
	fails(t, "committing a committed row", j.Commit(cctx, s3, todo.Edit{}))
	fails(t, "committing no row", j.Commit(cctx, 9999, todo.Edit{}))
	fails(t, "dropping a committed row", j.Drop(cctx, s1))
	fails(t, "dropping no row", j.Drop(cctx, 9999))

	s4, s5 := begin(t, j, a3), begin(t, j, a3)
	must(t, j.Stage(cctx, s4))
	must(t, j.Drop(cctx, s4))
	must(t, j.Drop(cctx, s5))
	same(t, "pending A after drops", pending(t, j, "A"), []todo.Change(nil))
	same(t, "pending B", pending(t, j, "B"), []todo.Change{as(b2, s2, todo.StatePending)})
	same(t, "committed A", after(t, j, "A", 0, 10), []todo.Change{as(a1, s1, todo.StateCommitted), as(a3, s3, todo.StateCommitted)})
	same(t, "head A", head(t, j, "A"), s3)
	same(t, "head B, only pending", head(t, j, "B"), int64(0))
	same(t, "head of no store", head(t, j, "none"), int64(0))
	same(t, "a dropped seq is never given again", begin(t, j, a3) > s5, true)
}

func conformRecord(t *testing.T, h Harness) {
	j := h.New(t)
	s1 := begin(t, j, full("A", "p"))
	c := full("A", "h")
	x := todo.SnapshotRow{Path: "open/h.md", ID: "h", Rev: "r2", Status: "open", Fields: map[string]string{"title": "d1"}}
	r := record(t, j, c, todo.Edit{Store: "A", Put: []todo.SnapshotRow{x}})
	same(t, "a recorded seq follows", r > s1, true)
	same(t, "head", head(t, j, "A"), r)
	same(t, "committed", after(t, j, "A", 0, 10), []todo.Change{as(c, r, todo.StateCommitted)})
	same(t, "pending", pending(t, j, "A"), []todo.Change{as(full("A", "p"), s1, todo.StatePending)})
	same(t, "the edit", snapshot(t, j, "A"), []todo.SnapshotRow{x})
}

func conformAfter(t *testing.T, h Harness) {
	j := h.New(t)
	var a []todo.Change
	for i := range 5 {
		c := full("A", string(rune('a'+i)))
		a = append(a, as(c, record(t, j, c, todo.Edit{}), todo.StateCommitted))
		record(t, j, full("B", "b"), todo.Edit{})
	}
	begin(t, j, full("A", "pending"))
	p1 := after(t, j, "A", 0, 2)
	same(t, "page 1", p1, a[:2])
	p2 := after(t, j, "A", p1[1].Seq, 2)
	same(t, "page 2", p2, a[2:4])
	same(t, "page 3", after(t, j, "A", p2[1].Seq, 2), a[4:])
	same(t, "after the head", after(t, j, "A", a[4].Seq, 10), []todo.Change(nil))
	same(t, "no room", after(t, j, "A", 0, 0), []todo.Change(nil))
}

func conformPrune(t *testing.T, h Harness) {
	j := h.New(t)
	old, now := t0.Add(-40*24*time.Hour), t0
	at := func(c todo.Change, when time.Time) todo.Change { c.At = when; return c }
	o1 := record(t, j, at(full("A", "1"), old), todo.Edit{})
	o2 := record(t, j, at(full("A", "2"), time.Time{}), todo.Edit{}) // no time: older than any
	o3 := record(t, j, at(full("A", "3"), old), todo.Edit{})
	n4 := record(t, j, at(full("A", "4"), now), todo.Edit{})
	p := begin(t, j, at(full("A", "p"), old))
	b := record(t, j, at(full("B", "b"), old), todo.Edit{})
	cut := t0.Add(-30 * 24 * time.Hour)

	same(t, "a prune before the zero time", prune(t, j, "A", time.Time{}, math.MaxInt64), 0)
	same(t, "pruned up to o2", prune(t, j, "A", cut, o2), 2)
	same(t, "left", seqs(after(t, j, "A", 0, 10)), []int64{o3, n4})
	same(t, "pruned the rest that is old", prune(t, j, "A", cut, math.MaxInt64), 1)
	same(t, "a pending row stays", seqs(pending(t, j, "A")), []int64{p})
	same(t, "another store's stay", seqs(after(t, j, "B", 0, 10)), []int64{b})
	same(t, "the head pruned", prune(t, j, "A", now.Add(time.Hour), math.MaxInt64), 1)
	same(t, "no head", head(t, j, "A"), int64(0))
	same(t, "a pruned seq is never given again", begin(t, j, full("A", "n")) > max(o1, n4, b), true)
}

func conformSnapshot(t *testing.T, h Harness) {
	j := h.New(t)
	x := todo.SnapshotRow{Path: "open/x.md", ID: "x", Rev: "1", Status: "open", Assignee: "agent:a",
		Fields: map[string]string{"title": "d1", "description": "d2"}}
	y := todo.SnapshotRow{Path: "open/y.md", ID: "y", Rev: "1", Status: "open"}
	z := todo.SnapshotRow{Path: "deferred/z.md", ID: "z", Rev: "1", Status: "deferred"}
	same(t, "none", snapshot(t, j, "A"), []todo.SnapshotRow(nil))
	must(t, j.Baseline(cctx, "A", []todo.SnapshotRow{y, x}))
	must(t, j.Baseline(cctx, "B", []todo.SnapshotRow{z}))
	same(t, "baseline, by path", snapshot(t, j, "A"), []todo.SnapshotRow{x, y})

	x2, w := x, todo.SnapshotRow{Path: "completed/w.md", ID: "w", Rev: "9", Status: "completed"}
	x2.Rev, x2.Fields = "2", map[string]string{"title": "d3"}
	must(t, j.Commit(cctx, begin(t, j, full("A", "x")), todo.Edit{Store: "A", Delete: []string{"open/y.md"}, Put: []todo.SnapshotRow{x2, w}}))
	same(t, "after an edit", snapshot(t, j, "A"), []todo.SnapshotRow{w, x2})

	must(t, j.Baseline(cctx, "A", []todo.SnapshotRow{x, x2}))
	same(t, "a path twice: the last", snapshot(t, j, "A"), []todo.SnapshotRow{x2})
	must(t, j.Baseline(cctx, "A", nil))
	same(t, "emptied", snapshot(t, j, "A"), []todo.SnapshotRow(nil))
	same(t, "another store's", snapshot(t, j, "B"), []todo.SnapshotRow{z})
}

func conformConsumers(t *testing.T, h Harness) {
	j := h.New(t)
	same(t, "none", consumers(t, j, "A"), []todo.Consumer(nil))
	a := todo.Consumer{Name: "auto-core", Acked: 3, SeenAt: t0.In(pdt)}
	b := todo.Consumer{Name: "a-tui", Unregistered: true, Resync: 7, SeenAt: t0}
	must(t, j.PutConsumer(cctx, "A", a))
	must(t, j.PutConsumer(cctx, "A", b))
	must(t, j.PutConsumer(cctx, "B", a))
	a.Acked, a.SeenAt = 5, t0.Add(time.Hour)
	must(t, j.PutConsumer(cctx, "A", a))
	same(t, "A's, by name", consumers(t, j, "A"), []todo.Consumer{b, a})
	same(t, "B's", consumers(t, j, "B"), []todo.Consumer{{Name: "auto-core", Acked: 3, SeenAt: t0}})
}

// settings is every field of each record set, and one record with none of its times.
func fullSettings() todo.Settings {
	return todo.Settings{
		Stores: map[string]todo.StoreRecord{
			"s1": {ID: "s1", Path: "/p/.todo-list", Claimed: true, ClaimedAt: t0, LastUse: t0.Add(time.Minute).In(pdt),
				RefreshedAt: t0.Add(time.Hour)},
			"s2": {ID: "s2", Path: "/elsewhere/todos"},
		},
		Roots: map[string]todo.RootRecord{
			"/p": {Root: "/p", Registered: t0, KBRoot: "/kb", LastResolved: t0.Add(time.Second)},
			"/q": {Root: "/q", Override: "/elsewhere/todos"},
		},
		Vars: map[string]string{"PROJ": "/home/u/proj", "X": "1"},
	}
}

func conformSettings(t *testing.T, h Harness) {
	j := h.New(t)
	same(t, "none", settings(t, j), todo.Settings{})
	want := fullSettings()
	must(t, j.UpdateSettings(cctx, func(s *todo.Settings) error { *s = fullSettings(); return nil }))
	same(t, "saved", settings(t, j), want)

	must(t, j.UpdateSettings(cctx, func(s *todo.Settings) error {
		same(t, "fn sees what is saved", *s, want)
		delete(s.Stores, "s2")
		delete(s.Vars, "X")
		s.Vars["Y"] = "2"
		r := s.Roots["/p"]
		r.Override = "/p/todos"
		s.Roots["/p"] = r
		return nil
	}))
	delete(want.Stores, "s2")
	delete(want.Vars, "X")
	want.Vars["Y"] = "2"
	want.Roots["/p"] = todo.RootRecord{Root: "/p", Override: "/p/todos", Registered: t0, KBRoot: "/kb", LastResolved: t0.Add(time.Second)}
	same(t, "changed", settings(t, j), want)

	err := j.UpdateSettings(cctx, func(s *todo.Settings) error {
		s.Stores = nil
		s.Vars["Z"] = "3"
		return errFn
	})
	same(t, "fn's error", errors.Is(err, errFn), true)
	same(t, "nothing saved", settings(t, j), want)
}

func conformRepath(t *testing.T, h Harness) {
	j := h.New(t)
	st := todo.Settings{
		Stores: map[string]todo.StoreRecord{"S": {ID: "S", Path: "/p/.todo-list", Claimed: true, ClaimedAt: t0},
			"T": {ID: "T", Path: "/q/.todo-list"}},
		Roots: map[string]todo.RootRecord{"/p": {Root: "/p", Registered: t0}, "/o": {Root: "/o", Override: "/p/.todo-list"},
			"/q": {Root: "/q"}},
	}
	must(t, j.UpdateSettings(cctx, func(s *todo.Settings) error { *s = st; return nil }))
	x := todo.SnapshotRow{Path: "open/x.md", ID: "x", Rev: "1", Status: "open"}
	must(t, j.Baseline(cctx, "S", []todo.SnapshotRow{x}))
	must(t, j.PutConsumer(cctx, "S", todo.Consumer{Name: "auto-core", Acked: 1, SeenAt: t0}))
	r1 := record(t, j, full("S", "x"), todo.Edit{})

	mv := full("S", "")
	mv.Kind, mv.Op, mv.From, mv.To = todo.KindMoveStore, todo.OpStoreMove, "/p/.todo-list", "/new/todos"
	seq := begin(t, j, mv)
	must(t, j.Commit(cctx, seq, todo.Edit{Store: "S", Repath: &todo.Repath{From: "/p/.todo-list", To: "/new/todos"}}))
	// a root the daemon had no record of, whose default store moves
	r3 := record(t, j, full("T", ""), todo.Edit{Store: "T", Repath: &todo.Repath{From: "/u/.todo-list", To: "/v"}})

	st.Stores["S"] = todo.StoreRecord{ID: "S", Path: "/new/todos", Claimed: true, ClaimedAt: t0}
	st.Stores["T"] = todo.StoreRecord{ID: "T", Path: "/v"}
	st.Roots["/p"] = todo.RootRecord{Root: "/p", Override: "/new/todos", Registered: t0}
	st.Roots["/o"] = todo.RootRecord{Root: "/o", Override: "/new/todos"}
	st.Roots["/u"] = todo.RootRecord{Root: "/u", Override: "/v"}
	same(t, "re-pointed", settings(t, j), st)
	same(t, "the snapshot, by id", snapshot(t, j, "S"), []todo.SnapshotRow{x})
	same(t, "the consumers, by id", consumers(t, j, "S"), []todo.Consumer{{Name: "auto-core", Acked: 1, SeenAt: t0}})
	same(t, "the log, by id", seqs(after(t, j, "S", 0, 10)), []int64{r1, seq})
	same(t, "T's log", seqs(after(t, j, "T", 0, 10)), []int64{r3})
}

func conformAtomic(t *testing.T, h Harness) {
	if h.Break == nil {
		t.Skip("the harness cannot break a transaction")
	}
	j := h.New(t)
	x := todo.SnapshotRow{Path: "open/x.md", ID: "x", Rev: "1", Status: "open"}
	y := todo.SnapshotRow{Path: "open/y.md", ID: "y", Rev: "1", Status: "open"}
	st := todo.Settings{Stores: map[string]todo.StoreRecord{"A": {ID: "A", Path: "/a/.todo-list"}},
		Roots: map[string]todo.RootRecord{}, Vars: map[string]string{}}
	must(t, j.UpdateSettings(cctx, func(s *todo.Settings) error { *s = st; return nil }))
	must(t, j.Baseline(cctx, "A", []todo.SnapshotRow{x}))
	c := full("A", "x")
	seq := begin(t, j, c)
	edit := todo.Edit{Store: "A", Delete: []string{x.Path}, Put: []todo.SnapshotRow{y}, Repath: &todo.Repath{From: "/a/.todo-list", To: "/b"}}

	undo := h.Break(t, j)
	fails(t, "a broken commit", j.Commit(cctx, seq, edit))
	_, err := j.Record(cctx, c, edit)
	fails(t, "a broken record", err)
	fails(t, "a broken update", j.UpdateSettings(cctx, func(s *todo.Settings) error {
		s.Stores["B"] = todo.StoreRecord{ID: "B", Path: "/b"}
		s.Roots["/b"] = todo.RootRecord{Root: "/b", Override: "/c"}
		s.Vars["V"] = "v"
		return nil
	}))
	same(t, "the row still pending", pending(t, j, "A"), []todo.Change{as(c, seq, todo.StatePending)})
	same(t, "no row committed", after(t, j, "A", 0, 10), []todo.Change(nil))
	same(t, "the snapshot as it was", snapshot(t, j, "A"), []todo.SnapshotRow{x})
	same(t, "the settings as they were", settings(t, j), st)

	undo()
	must(t, j.Commit(cctx, seq, edit))
	same(t, "the snapshot edited", snapshot(t, j, "A"), []todo.SnapshotRow{y})
	same(t, "the store moved", settings(t, j).Stores["A"].Path, "/b")
}

func conformReopen(t *testing.T, h Harness) {
	j := h.New(t)
	p := begin(t, j, full("A", "p"))
	s := begin(t, j, full("A", "s"))
	must(t, j.Stage(cctx, s))
	r := record(t, j, full("A", "r"), todo.Edit{Store: "A", Put: []todo.SnapshotRow{{Path: "open/r.md", ID: "r", Rev: "1",
		Status: "open", Fields: map[string]string{"title": "d"}}}})
	must(t, j.Drop(cctx, begin(t, j, full("A", "dropped"))))
	must(t, j.PutConsumer(cctx, "A", todo.Consumer{Name: "auto-core", Acked: r, SeenAt: t0}))
	must(t, j.UpdateSettings(cctx, func(st *todo.Settings) error { *st = fullSettings(); return nil }))
	wantPending, wantAfter, wantSnap := pending(t, j, "A"), after(t, j, "A", 0, 10), snapshot(t, j, "A")
	wantConsumers, wantSettings := consumers(t, j, "A"), settings(t, j)

	j = h.Reopen(t, j)
	same(t, "pending", pending(t, j, "A"), wantPending)
	same(t, "committed", after(t, j, "A", 0, 10), wantAfter)
	same(t, "head", head(t, j, "A"), r)
	same(t, "snapshot", snapshot(t, j, "A"), wantSnap)
	same(t, "consumers", consumers(t, j, "A"), wantConsumers)
	same(t, "settings", settings(t, j), wantSettings)
	same(t, "seqs go on rising", begin(t, j, full("A", "n")) > max(p, s, r)+1, true)
}

// THE CALLS, failing the test on an error

func begin(t *testing.T, j todo.Journal, c todo.Change) int64 {
	t.Helper()
	seq, err := j.Begin(cctx, c)
	must(t, err)
	return seq
}

func record(t *testing.T, j todo.Journal, c todo.Change, e todo.Edit) int64 {
	t.Helper()
	seq, err := j.Record(cctx, c, e)
	must(t, err)
	return seq
}

func pending(t *testing.T, j todo.Journal, store string) []todo.Change {
	t.Helper()
	out, err := j.Pending(cctx, store)
	must(t, err)
	return out
}

func after(t *testing.T, j todo.Journal, store string, seq int64, limit int) []todo.Change {
	t.Helper()
	out, err := j.After(cctx, store, seq, limit)
	must(t, err)
	return out
}

func head(t *testing.T, j todo.Journal, store string) int64 {
	t.Helper()
	n, err := j.Head(cctx, store)
	must(t, err)
	return n
}

func prune(t *testing.T, j todo.Journal, store string, before time.Time, upTo int64) int {
	t.Helper()
	n, err := j.Prune(cctx, store, before, upTo)
	must(t, err)
	return n
}

func snapshot(t *testing.T, j todo.Journal, store string) []todo.SnapshotRow {
	t.Helper()
	out, err := j.Snapshot(cctx, store)
	must(t, err)
	return out
}

func consumers(t *testing.T, j todo.Journal, store string) []todo.Consumer {
	t.Helper()
	out, err := j.Consumers(cctx, store)
	must(t, err)
	return out
}

func settings(t *testing.T, j todo.Journal) todo.Settings {
	t.Helper()
	s, err := j.Settings(cctx)
	must(t, err)
	return s
}

func seqs(cs []todo.Change) []int64 {
	var out []int64
	for _, c := range cs {
		out = append(out, c.Seq)
	}
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func fails(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: no error", what)
	}
}

func same(t *testing.T, what string, got, want any) {
	t.Helper()
	if g, w := norm(got), norm(want); !reflect.DeepEqual(g, w) {
		t.Errorf("%s:\n got  %+v\n want %+v", what, g, w)
	}
}

// norm is v with each time an instant in UTC, and an empty list or map none (a Settings' maps
// always there).
func norm(v any) any {
	switch v := v.(type) {
	case []todo.Change:
		var out []todo.Change
		for _, c := range v {
			c.At = instant(c.At)
			if len(c.Fields) == 0 {
				c.Fields = nil
			}
			out = append(out, c)
		}
		return out
	case []todo.SnapshotRow:
		var out []todo.SnapshotRow
		for _, r := range v {
			if len(r.Fields) == 0 {
				r.Fields = nil
			}
			out = append(out, r)
		}
		return out
	case []todo.Consumer:
		var out []todo.Consumer
		for _, c := range v {
			c.SeenAt = instant(c.SeenAt)
			out = append(out, c)
		}
		return out
	case todo.Settings:
		out := todo.Settings{Stores: map[string]todo.StoreRecord{}, Roots: map[string]todo.RootRecord{}, Vars: map[string]string{}}
		for k, r := range v.Stores {
			r.ClaimedAt, r.LastUse, r.RefreshedAt = instant(r.ClaimedAt), instant(r.LastUse), instant(r.RefreshedAt)
			out.Stores[k] = r
		}
		for k, r := range v.Roots {
			r.Registered, r.LastResolved = instant(r.Registered), instant(r.LastResolved)
			out.Roots[k] = r
		}
		for k, val := range v.Vars {
			out.Vars[k] = val
		}
		return out
	}
	return v
}

func instant(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Round(0)
}
