package sqlitecrewmail

import (
	"context"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
)

// Statement is one statement a store's tables rendered: its SQL and arguments.
type Statement struct {
	Op    dao.Op
	Table string
	SQL   string
	Args  []any
}

// Statements is what the stores opened while it records rendered: every statement since Reset.
type Statements struct {
	mu   sync.Mutex
	list []Statement
}

func (s *Statements) Reset() { s.mu.Lock(); s.list = nil; s.mu.Unlock() }

// Reads are the reads recorded since Reset.
func (s *Statements) Reads() []Statement {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Statement
	for _, st := range s.list {
		switch st.Op {
		case dao.OpSelect, dao.OpGet, dao.OpCount, dao.OpExists:
			out = append(out, st)
		}
	}
	return out
}

type recordHook struct {
	dao.NopHook
	s *Statements
}

func (h recordHook) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	h.s.mu.Lock()
	h.s.list = append(h.s.list, Statement{Op: q.Op, Table: q.Table, SQL: q.SQL, Args: append([]any(nil), q.Args...)})
	h.s.mu.Unlock()
	return nil
}

// RecordStatements records the statements of every store opened from now until the cell ends (its
// tables are built at the open); it is undone at the cell's end.
func RecordStatements(t *testing.T) *Statements {
	t.Helper()
	s := &Statements{}
	was := statementHooks
	statementHooks = []dao.Hook{recordHook{s: s}}
	t.Cleanup(func() { statementHooks = was })
	return s
}
