package store

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func workspaceRow(t *testing.T, s *Store, id int64) Workspace {
	t.Helper()
	list, err := s.Workspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range list {
		if w.ID == id {
			return w.Workspace
		}
	}
	t.Fatalf("no workspace %d", id)
	return Workspace{}
}

func TestAddWorkspace_GivesEachAUID(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	a, _ := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	b, _ := s.AddWorkspace(ctx, "b", "/b", nil, nil)
	ra, rb := workspaceRow(t, s, a.ID), workspaceRow(t, s, b.ID)
	if ra.UID == nil || rb.UID == nil || len(*ra.UID) != 32 || *ra.UID == *rb.UID {
		t.Errorf("uids %v and %v: want two distinct 32-hex identities", ra.UID, rb.UID)
	}
	if ra.Destination != DestinationLocal {
		t.Errorf("a new workspace's destination is %q, want the local store", ra.Destination)
	}
}

func TestConfigure_SavesEveryFieldTogether(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w, _ := s.AddWorkspace(ctx, "docs", "/docs", nil, nil)
	err := s.Configure(ctx, w.ID, Changes{
		Name:            ptr("kb"),
		Include:         ptr([]string{"**/*.md"}),
		Exclude:         ptr([]string{".git/**"}),
		SchemaPath:      ptr("schema.yaml"),
		TextExtensions:  ptr([]string{".log"}),
		SectionTokens:   ptr(256),
		EmbeddingPolicy: ptr(EmbeddingNever),
		Destination:     ptr(DestinationPostgres),
		VectorIndex:     ptr(IndexIVFFlat),
		ViewArgs:        ptr(map[string]any{"LabelGroupID": float64(7)}),
		Source:          &ConnectionSpec{Engine: EngineSQLite, DSN: "/data/catalog.db"},
		DestinationConn: &ConnectionSpec{Engine: EnginePostgres, DSN: "postgres://me:secret@db:5432/rag", Schema: "autodoc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := workspaceRow(t, s, w.ID)
	if r.Name != "kb" || *r.SchemaPath != "schema.yaml" || *r.TextExtensions != `[".log"]` || *r.SectionTokens != 256 ||
		r.EmbeddingPolicy != EmbeddingNever || r.Destination != DestinationPostgres || *r.VectorIndex != IndexIVFFlat ||
		*r.ViewArgs != `{"LabelGroupID":7}` {
		t.Errorf("row = %+v", r)
	}
	info, found, err := s.Connection(ctx, w.ID, RoleDestination)
	if err != nil || !found || info.DSN != "postgres://me:secret@db:5432/rag" || info.Schema != "autodoc" {
		t.Errorf("destination = %+v, %v, %v", info, found, err)
	}
}

// One refused field leaves the workspace exactly as it was: the valid fields saved with it are
// not written either.
func TestConfigure_IsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w, _ := s.AddWorkspace(ctx, "docs", "/docs", nil, nil)
	before := workspaceRow(t, s, w.ID)
	for name, c := range map[string]Changes{
		"postgres without its connection": {Name: ptr("renamed"), Destination: ptr(DestinationPostgres)},
		"a vector index on sqlite":        {Name: ptr("renamed"), VectorIndex: ptr(IndexHNSW)},
		"an unknown provider":             {Name: ptr("renamed"), Provider: ptr("nope")},
		"a section out of range":          {Name: ptr("renamed"), SectionTokens: ptr(64)},
		"an unknown policy":               {Name: ptr("renamed"), EmbeddingPolicy: ptr("sometimes")},
		"an unknown destination":          {Name: ptr("renamed"), Destination: ptr("mysql")},
		"an unknown vector index":         {Name: ptr("renamed"), VectorIndex: ptr("flat")},
		"include without exclude":         {Name: ptr("renamed"), Include: ptr([]string{"*.md"})},
		"a sqlite destination connection": {Name: ptr("renamed"), DestinationConn: &ConnectionSpec{Engine: EngineSQLite, DSN: "/x.db"}},
		"a source in another engine":      {Name: ptr("renamed"), Source: &ConnectionSpec{Engine: "mysql", DSN: "x"}},
		"a new connection with no DSN":    {Name: ptr("renamed"), Source: &ConnectionSpec{Engine: EnginePostgres}},
	} {
		if err := s.Configure(ctx, w.ID, c); err == nil {
			t.Errorf("%s: saved", name)
		}
		after := workspaceRow(t, s, w.ID)
		after.UpdatedAt = before.UpdatedAt
		if !reflect.DeepEqual(after, before) {
			t.Errorf("%s: the workspace changed: %+v, was %+v", name, after, before)
		}
		if _, found, _ := s.Connection(ctx, w.ID, RoleSource); found {
			t.Errorf("%s: a source connection was stored", name)
		}
	}
	if err := s.Configure(ctx, w.ID, Changes{Destination: ptr(DestinationPostgres)}); !errors.Is(err, ErrSetting) {
		t.Errorf("err = %v, want ErrSetting", err)
	}
	if err := s.Configure(ctx, 999, Changes{Name: ptr("x")}); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("a missing workspace: %v", err)
	}
}

// A DSN is sealed: the stored bytes are not the DSN, a sealed DSN copied to another workspace
// opens as nothing, and a client's summary carries no password.
func TestConnections_AreSealedAndSummarized(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	a, _ := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	b, _ := s.AddWorkspace(ctx, "b", "/b", nil, nil)
	const dsn = "postgres://me:hunter2@db.local:5432/catalog?sslmode=disable"
	if err := s.Configure(ctx, a.ID, Changes{Source: &ConnectionSpec{Engine: EnginePostgres, DSN: dsn}}); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	err := s.Read(ctx, func(tx *Tx) error {
		c, err := s.Workspace(a.ID).Connections(tx).With(ConnRole, RoleSource).Get()
		sealed = c.DSN
		return err
	})
	if err != nil || bytes.Contains(sealed, []byte("hunter2")) || bytes.Contains(sealed, []byte("db.local")) {
		t.Fatalf("stored DSN %q, %v: not sealed", sealed, err)
	}
	err = s.Write(ctx, func(tx *Tx) error {
		_, err := s.Workspace(b.ID).Connections(tx).Set(ConnRole, RoleSource).Set(ConnEngine, EnginePostgres).
			Set(ConnDSN, sealed).Set(ConnUpdatedAt, int64(0)).Insert()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Connection(ctx, b.ID, RoleSource); !errors.Is(err, ErrSealed) {
		t.Errorf("A's sealed DSN in B's row: %v, want ErrSealed", err)
	}
	sums, err := s.Connections(ctx, a.ID)
	if err != nil || len(sums) != 1 {
		t.Fatalf("summaries %+v, %v", sums, err)
	}
	if got := sums[0]; got.Role != RoleSource || got.Host != "db.local:5432" || got.Database != "catalog" || got.User != "me" || !got.HasPassword {
		t.Errorf("summary %+v", got)
	}
}

func TestSummarize(t *testing.T) {
	for _, tc := range []struct {
		engine, dsn string
		want        ConnectionSummary
	}{
		{EnginePostgres, "postgres://u@h/db", ConnectionSummary{Engine: EnginePostgres, Host: "h", Database: "db", User: "u"}},
		{EnginePostgres, "postgresql://u:p@h:6543/db", ConnectionSummary{Engine: EnginePostgres, Host: "h:6543", Database: "db", User: "u", HasPassword: true}},
		{EnginePostgres, "postgres://h/db?password=p", ConnectionSummary{Engine: EnginePostgres, Host: "h", Database: "db", HasPassword: true}},
		{EnginePostgres, "host=h port=5433 dbname=db user=u password='p q'", ConnectionSummary{Engine: EnginePostgres, Host: "h:5433", Database: "db", User: "u", HasPassword: true}},
		{EnginePostgres, "host=h dbname=db", ConnectionSummary{Engine: EnginePostgres, Host: "h", Database: "db"}},
		{EngineSQLite, "/data/catalog.db", ConnectionSummary{Engine: EngineSQLite, Database: "/data/catalog.db"}},
	} {
		if got := Summarize(tc.engine, tc.dsn); got != tc.want {
			t.Errorf("%s %q: %+v, want %+v", tc.engine, tc.dsn, got, tc.want)
		}
	}
}

// An empty DSN keeps the stored one, so a client edits a connection's schema without sending its
// secret again; Remove drops it; removing the workspace removes its connections.
func TestConnections_KeepRemoveCascade(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w, _ := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	if err := s.Configure(ctx, w.ID, Changes{Source: &ConnectionSpec{Engine: EnginePostgres, DSN: "postgres://u:p@h/db"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(ctx, w.ID, Changes{Source: &ConnectionSpec{Engine: EnginePostgres, Schema: "catalog"}}); err != nil {
		t.Fatal(err)
	}
	info, _, err := s.Connection(ctx, w.ID, RoleSource)
	if err != nil || info.DSN != "postgres://u:p@h/db" || info.Schema != "catalog" {
		t.Errorf("after a schema-only edit: %+v, %v", info, err)
	}
	if err := s.Configure(ctx, w.ID, Changes{Source: &ConnectionSpec{Remove: true}}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.Connection(ctx, w.ID, RoleSource); found {
		t.Error("the source survived its removal")
	}
	if err := s.Configure(ctx, w.ID, Changes{DestinationConn: &ConnectionSpec{Engine: EnginePostgres, DSN: "postgres://h/db"},
		Destination: ptr(DestinationPostgres)}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveWorkspace(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	err = s.Read(ctx, func(tx *Tx) error {
		n, err := tx.t.connections.On(tx.tx).Count()
		if err == nil && n != 0 {
			t.Errorf("%d connections outlived their workspace", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Dropping the destination's connection while it is still Postgres is refused; moving back to
// the local store first is not.
func TestConfigure_KeepsTheDestinationCoherent(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w, _ := s.AddWorkspace(ctx, "a", "/a", nil, nil)
	if err := s.Configure(ctx, w.ID, Changes{DestinationConn: &ConnectionSpec{Engine: EnginePostgres, DSN: "postgres://h/db"},
		Destination: ptr(DestinationPostgres)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(ctx, w.ID, Changes{DestinationConn: &ConnectionSpec{Remove: true}}); err == nil ||
		!strings.Contains(err.Error(), "needs its connection") {
		t.Errorf("dropping a live destination's connection: %v", err)
	}
	if err := s.Configure(ctx, w.ID, Changes{Destination: ptr(DestinationLocal), DestinationConn: &ConnectionSpec{Remove: true}}); err != nil {
		t.Errorf("back to the local store: %v", err)
	}
}
