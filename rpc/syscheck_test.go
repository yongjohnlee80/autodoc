package rpc

import (
	"context"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// fixedCheck is a store check answering its report.
type fixedCheck store.CheckReport

func (c fixedCheck) Check(context.Context) (store.CheckReport, error) {
	return store.CheckReport(c), nil
}

// TestSysCheckWithNoCheckerIsUnsupported: a server given no store check (a store that is not
// SQLite's) answers sys.check unsupported, saying a PostgreSQL store is checked by its database
// server; given one, it answers that check's report.
func TestSysCheckWithNoCheckerIsUnsupported(t *testing.T) {
	cli, _, err := helloAs(serve(t), Protocol, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	_, err = cli.Call(context.Background(), "sys.check")
	if c, msg := wireError(err); c != CodeUnsupported ||
		msg != "this server's store has no check here: a PostgreSQL store is checked by its database server" {
		t.Fatalf("no checker: %d %q (%v), want unsupported, saying why", c, msg, err)
	}

	cli, _, err = helloAs(serveWith(t, WithStoreCheck(fixedCheck{Integrity: []string{"page 7: btree mismatch"}})), Protocol, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	res, err := cli.Call(context.Background(), "sys.check")
	if err != nil {
		t.Fatalf("with a checker: %v", err)
	}
	m, _ := res.(map[string]any)
	integrity, _ := m["integrity"].([]any)
	if len(integrity) != 1 || integrity[0] != "page 7: btree mismatch" || m["quick"] != false {
		t.Fatalf("with a checker: %#v, want its report", res)
	}
}
