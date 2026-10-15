package sqlitejournal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/todo"
)

// THE TODO JOURNAL ON THE STORE — core/todo's Journal port (ADR 1791532020 §2.5) on the store's
// todo tables (migration 000017; 000018 adds a consumer's resync, 000019 a store's daily refresh,
// 000020 a store's per-machine variables):
// each todo store's change log, snapshot and consumers' cursors, and the store, root and var
// records, all keyed by the store's stable id, never its path. The tables are declared through
// core/store's seam (store.Declare), so neither core/store nor core/todo imports the other: this
// package joins them.
//
// Every method is one transaction. A write runs on the store's single writer connection, so Commit
// and Record write their row and apply their Edit together, and UpdateSettings reads, changes and
// saves the records together: a failure anywhere leaves none of it. A read runs on a reader, in one
// snapshot of the WAL.
//
// A time is kept as Unix nanoseconds, NULL for the zero time, and read back in UTC: the instant
// round-trips, its zone does not.

// errTodoSettled is a Stage, Commit or Drop of a row that is not pending (Stage: not exactly
// pending), or not there.
var errTodoSettled = errs.Sentinel(errs.ErrPrecondition, "todo journal: the change is not pending")

// New is the todo system's journal on the store s.
func New(s *store.Store) todo.Journal { return &todoJournal{s: s} }

type todoJournal struct{ s *store.Store }

// tables are the todo tables, declared to the store: built for each store's writer and readers.
var tables = store.Declare("todo", newTodoTables)

var _ todo.Journal = (*todoJournal)(nil)

// todoCol names a column of a todo table.
type todoCol string

const (
	tcStore        todoCol = "store"
	tcID           todoCol = "id"
	tcPath         todoCol = "path"
	tcSeq          todoCol = "seq"
	tcKind         todoCol = "kind"
	tcOp           todoCol = "op"
	tcFrom         todoCol = "from_value"
	tcTo           todoCol = "to_value"
	tcFields       todoCol = "fields"
	tcReason       todoCol = "reason"
	tcHand         todoCol = "hand"
	tcFromPath     todoCol = "from_path"
	tcToPath       todoCol = "to_path"
	tcFromRev      todoCol = "from_rev"
	tcToRev        todoCol = "to_rev"
	tcState        todoCol = "state"
	tcAt           todoCol = "at"
	tcRev          todoCol = "rev"
	tcStatus       todoCol = "status"
	tcAssignee     todoCol = "assignee"
	tcName         todoCol = "name"
	tcAcked        todoCol = "acked_seq"
	tcSeenAt       todoCol = "seen_at"
	tcUnregistered todoCol = "unregistered"
	tcResync       todoCol = "resync_seq"
	tcClaimed      todoCol = "claimed"
	tcClaimedAt    todoCol = "claimed_at"
	tcLastUse      todoCol = "last_use"
	tcRefreshedAt  todoCol = "refreshed_at"
	tcRoot         todoCol = "root"
	tcOverride     todoCol = "override"
	tcRegistered   todoCol = "registered"
	tcKBRoot       todoCol = "kb_root"
	tcLastResolved todoCol = "last_resolved"
	tcValue        todoCol = "value"
)

// The rows, as the tables hold them.
type (
	todoStoreRow struct {
		ID, Path                        string
		Claimed                         int64
		ClaimedAt, LastUse, RefreshedAt *int64
	}
	todoRootRow struct {
		Root, Override, KBRoot   string
		Registered, LastResolved *int64
	}
	todoVarRow      struct{ Name, Value string }
	todoStoreVarRow struct{ Store, Name, Value string }
	todoChangeRow   struct {
		Seq                        int64
		Store, ID, Kind, Op, State string
		From, To, Reason           string
		FromPath, ToPath           string
		FromRev, ToRev             string
		Fields                     *string
		Hand                       int64
		At                         *int64
	}
	todoSnapRow struct {
		Store, Path, ID, Rev, Status, Assignee string
		Fields                                 *string
	}
	todoConsumerRow struct {
		Store, Name  string
		Acked        int64
		SeenAt       *int64
		Unregistered int64
		Resync       int64
	}
)

// todoSort names a todo table's order.
type todoSort string

const (
	byKey  todoSort = "key"
	bySeq  todoSort = "seq"
	byPath todoSort = "path"
)

type todoSchema[R any] = dao.Schema[R, todoCol, todoSort, int64]

// todoTables is every todo table, bound to one connection.
type todoTables struct {
	stores    *todoSchema[*todoStoreRow]
	roots     *todoSchema[*todoRootRow]
	vars      *todoSchema[*todoVarRow]
	storeVars *todoSchema[*todoStoreVarRow]
	changes   *todoSchema[*todoChangeRow]
	snapshot  *todoSchema[*todoSnapRow]
	consumers *todoSchema[*todoConsumerRow]
}

// todoTable declares one todo table: its columns, the columns an upsert conflicts on, and its
// order; an id column, when there is one, is what Insert returns.
func todoTable[R any](c dao.DataConn, table string, fields map[todoCol]func(R) any, conflict []todoCol,
	order map[todoSort]string, id ...todoCol) *todoSchema[R] {
	fs := map[todoCol]dao.Field[R]{}
	for name, scan := range fields {
		fs[name] = store.Col(table, name, scan)
	}
	opts := []dao.Option[R, todoCol, todoSort, int64]{
		dao.Table[R, todoCol, todoSort, int64](table),
		dao.Fields[R, todoCol, todoSort, int64](fs),
		dao.SortMap[R, todoCol, todoSort, int64](order),
	}
	if len(conflict) > 0 {
		opts = append(opts, dao.Conflict[R, todoCol, todoSort, int64](conflict...))
	}
	if len(id) > 0 {
		opts = append(opts, dao.ID[R, todoCol, todoSort, int64](id[0]))
	}
	return dao.New(c, opts...)
}

func newTodoTables(c dao.DataConn) *todoTables {
	return &todoTables{
		stores: todoTable(c, "todo_store", map[todoCol]func(*todoStoreRow) any{
			tcID:          func(r *todoStoreRow) any { return &r.ID },
			tcPath:        func(r *todoStoreRow) any { return &r.Path },
			tcClaimed:     func(r *todoStoreRow) any { return &r.Claimed },
			tcClaimedAt:   func(r *todoStoreRow) any { return &r.ClaimedAt },
			tcLastUse:     func(r *todoStoreRow) any { return &r.LastUse },
			tcRefreshedAt: func(r *todoStoreRow) any { return &r.RefreshedAt },
		}, []todoCol{tcID}, map[todoSort]string{byKey: `"todo_store"."id"`}),
		roots: todoTable(c, "todo_root", map[todoCol]func(*todoRootRow) any{
			tcRoot:         func(r *todoRootRow) any { return &r.Root },
			tcOverride:     func(r *todoRootRow) any { return &r.Override },
			tcRegistered:   func(r *todoRootRow) any { return &r.Registered },
			tcKBRoot:       func(r *todoRootRow) any { return &r.KBRoot },
			tcLastResolved: func(r *todoRootRow) any { return &r.LastResolved },
		}, []todoCol{tcRoot}, map[todoSort]string{byKey: `"todo_root"."root"`}),
		vars: todoTable(c, "todo_var", map[todoCol]func(*todoVarRow) any{
			tcName:  func(r *todoVarRow) any { return &r.Name },
			tcValue: func(r *todoVarRow) any { return &r.Value },
		}, []todoCol{tcName}, map[todoSort]string{byKey: `"todo_var"."name"`}),
		storeVars: todoTable(c, "todo_store_var", map[todoCol]func(*todoStoreVarRow) any{
			tcStore: func(r *todoStoreVarRow) any { return &r.Store },
			tcName:  func(r *todoStoreVarRow) any { return &r.Name },
			tcValue: func(r *todoStoreVarRow) any { return &r.Value },
		}, []todoCol{tcStore, tcName}, map[todoSort]string{byKey: `"todo_store_var"."name"`}),
		changes: todoTable(c, "todo_change", map[todoCol]func(*todoChangeRow) any{
			tcSeq:      func(r *todoChangeRow) any { return &r.Seq },
			tcStore:    func(r *todoChangeRow) any { return &r.Store },
			tcID:       func(r *todoChangeRow) any { return &r.ID },
			tcKind:     func(r *todoChangeRow) any { return &r.Kind },
			tcOp:       func(r *todoChangeRow) any { return &r.Op },
			tcFrom:     func(r *todoChangeRow) any { return &r.From },
			tcTo:       func(r *todoChangeRow) any { return &r.To },
			tcFields:   func(r *todoChangeRow) any { return &r.Fields },
			tcReason:   func(r *todoChangeRow) any { return &r.Reason },
			tcHand:     func(r *todoChangeRow) any { return &r.Hand },
			tcFromPath: func(r *todoChangeRow) any { return &r.FromPath },
			tcToPath:   func(r *todoChangeRow) any { return &r.ToPath },
			tcFromRev:  func(r *todoChangeRow) any { return &r.FromRev },
			tcToRev:    func(r *todoChangeRow) any { return &r.ToRev },
			tcState:    func(r *todoChangeRow) any { return &r.State },
			tcAt:       func(r *todoChangeRow) any { return &r.At },
		}, nil, map[todoSort]string{bySeq: `"todo_change"."seq"`}, tcSeq),
		snapshot: todoTable(c, "todo_snapshot", map[todoCol]func(*todoSnapRow) any{
			tcStore:    func(r *todoSnapRow) any { return &r.Store },
			tcPath:     func(r *todoSnapRow) any { return &r.Path },
			tcID:       func(r *todoSnapRow) any { return &r.ID },
			tcRev:      func(r *todoSnapRow) any { return &r.Rev },
			tcStatus:   func(r *todoSnapRow) any { return &r.Status },
			tcAssignee: func(r *todoSnapRow) any { return &r.Assignee },
			tcFields:   func(r *todoSnapRow) any { return &r.Fields },
		}, []todoCol{tcStore, tcPath}, map[todoSort]string{byPath: `"todo_snapshot"."path"`}),
		consumers: todoTable(c, "todo_consumer", map[todoCol]func(*todoConsumerRow) any{
			tcStore:        func(r *todoConsumerRow) any { return &r.Store },
			tcName:         func(r *todoConsumerRow) any { return &r.Name },
			tcAcked:        func(r *todoConsumerRow) any { return &r.Acked },
			tcSeenAt:       func(r *todoConsumerRow) any { return &r.SeenAt },
			tcUnregistered: func(r *todoConsumerRow) any { return &r.Unregistered },
			tcResync:       func(r *todoConsumerRow) any { return &r.Resync },
		}, []todoCol{tcStore, tcName}, map[todoSort]string{byKey: `"todo_consumer"."name"`}),
	}
}

// write runs fn in a write transaction on the writer; read, in a read transaction on a reader.
func (j *todoJournal) write(ctx context.Context, fn func(tx *dao.Transaction, t *todoTables) error) error {
	return j.s.Write(ctx, func(tx *store.Tx) error { return fn(tx.Transaction(), tables.In(tx)) })
}

func (j *todoJournal) read(ctx context.Context, fn func(tx *dao.Transaction, t *todoTables) error) error {
	return j.s.Read(ctx, func(tx *store.Tx) error { return fn(tx.Transaction(), tables.In(tx)) })
}

// THE CHANGE LOG

func (j *todoJournal) Begin(ctx context.Context, c todo.Change) (int64, error) {
	var seq int64
	err := j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		var err error
		seq, err = insertChange(tx, t, c, todo.StatePending)
		return err
	})
	return seq, err
}

func (j *todoJournal) Stage(ctx context.Context, seq int64) error {
	return j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		if err := settle(tx, t, seq, "stage", todo.StatePending); err != nil {
			return err
		}
		return t.changes.On(tx).With(tcSeq, seq).Set(tcState, todo.StateStaged).Update()
	})
}

func (j *todoJournal) Commit(ctx context.Context, seq int64, e todo.Edit) error {
	return j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		if err := settle(tx, t, seq, "commit", todo.StatePending, todo.StateStaged); err != nil {
			return err
		}
		if err := t.changes.On(tx).With(tcSeq, seq).Set(tcState, todo.StateCommitted).Update(); err != nil {
			return err
		}
		return applyEdit(tx, t, e)
	})
}

func (j *todoJournal) Drop(ctx context.Context, seq int64) error {
	return j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		if err := settle(tx, t, seq, "drop", todo.StatePending, todo.StateStaged); err != nil {
			return err
		}
		return t.changes.On(tx).With(tcSeq, seq).Delete()
	})
}

func (j *todoJournal) Pending(ctx context.Context, store string) ([]todo.Change, error) {
	var out []todo.Change
	err := j.read(ctx, func(tx *dao.Transaction, t *todoTables) error {
		var err error
		out, err = changes(t.changes.On(tx).With(tcStore, store).With(tcState, todo.StatePending, todo.StateStaged))
		return err
	})
	return out, err
}

func (j *todoJournal) Record(ctx context.Context, c todo.Change, e todo.Edit) (int64, error) {
	var seq int64
	err := j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		var err error
		if seq, err = insertChange(tx, t, c, todo.StateCommitted); err != nil {
			return err
		}
		return applyEdit(tx, t, e)
	})
	return seq, err
}

func (j *todoJournal) Head(ctx context.Context, store string) (int64, error) {
	var head int64
	err := j.read(ctx, func(tx *dao.Transaction, t *todoTables) error {
		rows, err := t.changes.On(tx).With(tcStore, store).With(tcState, todo.StateCommitted).
			OrderBy(dao.Desc(bySeq)).Limit(1).Select(tcSeq)
		if len(rows) == 1 {
			head = rows[0].Seq
		}
		return err
	})
	return head, err
}

func (j *todoJournal) After(ctx context.Context, store string, after int64, limit int) ([]todo.Change, error) {
	if limit <= 0 {
		return nil, nil
	}
	var out []todo.Change
	err := j.read(ctx, func(tx *dao.Transaction, t *todoTables) error {
		var err error
		out, err = changes(t.changes.On(tx).With(tcStore, store).With(tcState, todo.StateCommitted).
			WithPredicate(dao.Gt(`"todo_change"."seq"`, after)).Limit(uint64(limit)))
		return err
	})
	return out, err
}

func (j *todoJournal) Prune(ctx context.Context, store string, before time.Time, upTo int64) (int, error) {
	if before.IsZero() { // nothing is older than the zero time
		return 0, nil
	}
	var n uint64
	err := j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		old := func() dao.DAO[*todoChangeRow, todoCol, int64] {
			return t.changes.On(tx).With(tcStore, store).With(tcState, todo.StateCommitted).
				WithPredicate(dao.Lte(`"todo_change"."seq"`, upTo)).
				WithPredicate(dao.Or(dao.IsNull(`"todo_change"."at"`), dao.Lt(`"todo_change"."at"`, before.UnixNano())))
		}
		var err error
		if n, err = old().Count(); err != nil || n == 0 {
			return err
		}
		return old().Delete()
	})
	return int(n), err
}

// insertChange writes c as a new row in state, at the next seq.
func insertChange(tx *dao.Transaction, t *todoTables, c todo.Change, state string) (int64, error) {
	return t.changes.On(tx).SetMap(map[todoCol]any{
		tcStore: c.Store, tcID: c.ID, tcKind: c.Kind, tcOp: c.Op, tcFrom: c.From, tcTo: c.To,
		tcFields: jsonText(c.Fields, len(c.Fields)), tcReason: c.Reason, tcHand: flag(c.Hand), tcFromPath: c.FromPath, tcToPath: c.ToPath,
		tcFromRev: c.FromRev, tcToRev: c.ToRev, tcState: state, tcAt: nanos(c.At),
	}).Insert()
}

// settle checks that row seq is in one of states, for verb.
func settle(tx *dao.Transaction, t *todoTables, seq int64, verb string, states ...string) error {
	r, err := t.changes.On(tx).With(tcSeq, seq).Get(tcState)
	switch {
	case errors.Is(err, dao.ErrNoRows):
	case err != nil:
		return err
	default:
		for _, s := range states {
			if r.State == s {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s %d", errTodoSettled, verb, seq)
}

// changes is the rows q selects, in seq order.
func changes(q dao.DAO[*todoChangeRow, todoCol, int64]) ([]todo.Change, error) {
	rows, err := q.OrderBy(dao.Asc(bySeq)).Select()
	if err != nil {
		return nil, err
	}
	out := make([]todo.Change, 0, len(rows))
	for _, r := range rows {
		c := todo.Change{Seq: r.Seq, Store: r.Store, ID: r.ID, Kind: r.Kind, Op: r.Op, From: r.From, To: r.To,
			Reason: r.Reason, Hand: r.Hand != 0, FromPath: r.FromPath, ToPath: r.ToPath, FromRev: r.FromRev,
			ToRev: r.ToRev, State: r.State, At: fromNanos(r.At)}
		if err := fromJSON(r.Fields, &c.Fields); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// applyEdit is what a committed row changes besides itself: the snapshot's deletes, then its puts,
// then a store move's records.
func applyEdit(tx *dao.Transaction, t *todoTables, e todo.Edit) error {
	for _, p := range e.Delete {
		if err := t.snapshot.On(tx).With(tcStore, e.Store).With(tcPath, p).Delete(); err != nil {
			return err
		}
	}
	if err := putSnapshot(tx, t, e.Store, e.Put); err != nil {
		return err
	}
	if e.Repath == nil {
		return nil
	}
	before, err := loadSettings(tx, t)
	if err != nil {
		return err
	}
	after := cloneSettings(before)
	after.ApplyRepath(e.Store, *e.Repath)
	return saveSettings(tx, t, before, after)
}

// THE SNAPSHOT

func (j *todoJournal) Snapshot(ctx context.Context, store string) ([]todo.SnapshotRow, error) {
	var out []todo.SnapshotRow
	err := j.read(ctx, func(tx *dao.Transaction, t *todoTables) error {
		rows, err := t.snapshot.On(tx).With(tcStore, store).OrderBy(dao.Asc(byPath)).Select()
		if err != nil {
			return err
		}
		for _, r := range rows {
			s := todo.SnapshotRow{Path: r.Path, ID: r.ID, Rev: r.Rev, Status: r.Status, Assignee: r.Assignee}
			if err := fromJSON(r.Fields, &s.Fields); err != nil {
				return err
			}
			out = append(out, s)
		}
		return nil
	})
	return out, err
}

func (j *todoJournal) Baseline(ctx context.Context, store string, rows []todo.SnapshotRow) error {
	return j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		if err := t.snapshot.On(tx).With(tcStore, store).Delete(); err != nil {
			return err
		}
		return putSnapshot(tx, t, store, rows)
	})
}

// putSnapshot adds or replaces a store's rows, by path.
func putSnapshot(tx *dao.Transaction, t *todoTables, store string, rows []todo.SnapshotRow) error {
	for _, r := range rows {
		if err := t.snapshot.On(tx).SetMap(map[todoCol]any{tcStore: store, tcPath: r.Path, tcID: r.ID, tcRev: r.Rev,
			tcStatus: r.Status, tcAssignee: r.Assignee, tcFields: jsonText(r.Fields, len(r.Fields))}).Upsert(); err != nil {
			return err
		}
	}
	return nil
}

// THE CONSUMERS

func (j *todoJournal) Consumers(ctx context.Context, store string) ([]todo.Consumer, error) {
	var out []todo.Consumer
	err := j.read(ctx, func(tx *dao.Transaction, t *todoTables) error {
		rows, err := t.consumers.On(tx).With(tcStore, store).OrderBy(dao.Asc(byKey)).Select()
		for _, r := range rows {
			out = append(out, todo.Consumer{Name: r.Name, Acked: r.Acked, SeenAt: fromNanos(r.SeenAt), Unregistered: r.Unregistered != 0,
				Resync: r.Resync})
		}
		return err
	})
	return out, err
}

func (j *todoJournal) PutConsumer(ctx context.Context, store string, c todo.Consumer) error {
	return j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		return t.consumers.On(tx).SetMap(map[todoCol]any{tcStore: store, tcName: c.Name, tcAcked: c.Acked,
			tcSeenAt: nanos(c.SeenAt), tcUnregistered: flag(c.Unregistered), tcResync: c.Resync}).Upsert()
	})
}

// THE SETTINGS — a map's key is its record's column: a store's id, a root, a var's name.

func (j *todoJournal) Settings(ctx context.Context) (todo.Settings, error) {
	var s todo.Settings
	err := j.read(ctx, func(tx *dao.Transaction, t *todoTables) error {
		var err error
		s, err = loadSettings(tx, t)
		return err
	})
	return s, err
}

func (j *todoJournal) UpdateSettings(ctx context.Context, fn func(*todo.Settings) error) error {
	return j.write(ctx, func(tx *dao.Transaction, t *todoTables) error {
		before, err := loadSettings(tx, t)
		if err != nil {
			return err
		}
		after := cloneSettings(before)
		if err := fn(&after); err != nil {
			return err
		}
		return saveSettings(tx, t, before, after)
	})
}

func loadSettings(tx *dao.Transaction, t *todoTables) (todo.Settings, error) {
	s := todo.Settings{Stores: map[string]todo.StoreRecord{}, Roots: map[string]todo.RootRecord{}, Vars: map[string]string{}}
	stores, err := t.stores.On(tx).Select()
	if err != nil {
		return s, err
	}
	for _, r := range stores {
		s.Stores[r.ID] = todo.StoreRecord{ID: r.ID, Path: r.Path, Claimed: r.Claimed != 0,
			ClaimedAt: fromNanos(r.ClaimedAt), LastUse: fromNanos(r.LastUse), RefreshedAt: fromNanos(r.RefreshedAt)}
	}
	roots, err := t.roots.On(tx).Select()
	if err != nil {
		return s, err
	}
	for _, r := range roots {
		s.Roots[r.Root] = todo.RootRecord{Root: r.Root, Override: r.Override, Registered: fromNanos(r.Registered),
			KBRoot: r.KBRoot, LastResolved: fromNanos(r.LastResolved)}
	}
	vars, err := t.vars.On(tx).Select()
	if err != nil {
		return s, err
	}
	for _, r := range vars {
		s.Vars[r.Name] = r.Value
	}
	svars, err := t.storeVars.On(tx).Select()
	for _, r := range svars {
		if s.MachineVars == nil {
			s.MachineVars = map[string]map[string]string{}
		}
		if s.MachineVars[r.Store] == nil {
			s.MachineVars[r.Store] = map[string]string{}
		}
		s.MachineVars[r.Store][r.Name] = r.Value
	}
	return s, err
}

// saveSettings writes what after changed from before: each record added or changed, then each one
// gone, stores before roots before vars.
func saveSettings(tx *dao.Transaction, t *todoTables, before, after todo.Settings) error {
	err := syncRecords(after.Stores, before.Stores, func(id string, r todo.StoreRecord) error {
		return t.stores.On(tx).SetMap(map[todoCol]any{tcID: id, tcPath: r.Path, tcClaimed: flag(r.Claimed),
			tcClaimedAt: nanos(r.ClaimedAt), tcLastUse: nanos(r.LastUse), tcRefreshedAt: nanos(r.RefreshedAt)}).Upsert()
	}, func(id string) error { return t.stores.On(tx).With(tcID, id).Delete() })
	if err != nil {
		return err
	}
	err = syncRecords(after.Roots, before.Roots, func(root string, r todo.RootRecord) error {
		return t.roots.On(tx).SetMap(map[todoCol]any{tcRoot: root, tcOverride: r.Override, tcRegistered: nanos(r.Registered),
			tcKBRoot: r.KBRoot, tcLastResolved: nanos(r.LastResolved)}).Upsert()
	}, func(root string) error { return t.roots.On(tx).With(tcRoot, root).Delete() })
	if err != nil {
		return err
	}
	err = syncRecords(after.Vars, before.Vars, func(name, value string) error {
		return t.vars.On(tx).SetMap(map[todoCol]any{tcName: name, tcValue: value}).Upsert()
	}, func(name string) error { return t.vars.On(tx).With(tcName, name).Delete() })
	if err != nil {
		return err
	}
	return syncRecords(flatStoreVars(after.MachineVars), flatStoreVars(before.MachineVars), func(k, value string) error {
		store, name, _ := strings.Cut(k, "\x00")
		return t.storeVars.On(tx).SetMap(map[todoCol]any{tcStore: store, tcName: name, tcValue: value}).Upsert()
	}, func(k string) error {
		store, name, _ := strings.Cut(k, "\x00")
		return t.storeVars.On(tx).With(tcStore, store).With(tcName, name).Delete()
	})
}

// flatStoreVars keys each per-store value by its store id and name, NUL between them.
func flatStoreVars(mv map[string]map[string]string) map[string]string {
	out := map[string]string{}
	for store, vals := range mv {
		for name, v := range vals {
			out[store+"\x00"+name] = v
		}
	}
	return out
}

// syncRecords puts every entry of after that before does not hold as it is, and deletes every key of
// before that after lacks.
func syncRecords[V comparable](after, before map[string]V, put func(string, V) error, del func(string) error) error {
	for k, v := range after {
		if old, ok := before[k]; ok && old == v {
			continue
		}
		if err := put(k, v); err != nil {
			return err
		}
	}
	for k := range before {
		if _, ok := after[k]; ok {
			continue
		}
		if err := del(k); err != nil {
			return err
		}
	}
	return nil
}

func cloneSettings(s todo.Settings) todo.Settings {
	var mv map[string]map[string]string
	if s.MachineVars != nil {
		mv = make(map[string]map[string]string, len(s.MachineVars))
		for id, vals := range s.MachineVars {
			mv[id] = maps.Clone(vals)
		}
	}
	return todo.Settings{Stores: maps.Clone(s.Stores), Roots: maps.Clone(s.Roots), Vars: maps.Clone(s.Vars), MachineVars: mv}
}

// THE COLUMNS' ENCODINGS

func nanos(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	n := t.UnixNano()
	return &n
}

func fromNanos(n *int64) time.Time {
	if n == nil {
		return time.Time{}
	}
	return time.Unix(0, *n).UTC()
}

func flag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// jsonText is a list or a map as JSON text; nil for an empty one (n, its length). Neither a
// []string nor a map[string]string fails to encode.
func jsonText(v any, n int) *string {
	if n == 0 {
		return nil
	}
	b, _ := json.Marshal(v)
	s := string(b)
	return &s
}

// fromJSON decodes a JSON column into dst; NULL leaves it as it is.
func fromJSON(s *string, dst any) error {
	if s == nil {
		return nil
	}
	return json.Unmarshal([]byte(*s), dst)
}
