package index

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/sqlite"
)

// The chunk's full-text search is chosen by the engine's name: SQLite's FTS5, joined; and another
// engine, which has none declared, is refused while the tables are declared, naming it, rather than
// handed one engine's query syntax.
func TestFullTextIsTheEnginesOwn(t *testing.T) {
	if ft := fullTextOn(sqlite.SqliteDialect{}); ft.ix.Name != ChunkFTS.Name || !ft.join {
		t.Errorf("SQLite: %+v, want FTS5 joined", ft.ix)
	}
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "no full-text search is declared for the") {
			t.Errorf("another engine: %v, want a refusal naming it", r)
		}
	}()
	fullTextOn(dao.GenericDialect{})
}
