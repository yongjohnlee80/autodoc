package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// t0 is the fixture's first updated_at; rows are placed at t0 plus whole seconds.
var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// fixture is a business database, as an enterprise has one: a track and an artist table in public,
// with pgvector installed there, and pgstore in a schema of its own beside them, opened as a
// server opens it. Each test gets its own database.
type fixture struct {
	t        *testing.T
	ctx      context.Context
	dsn      string
	schema   string
	business dao.DataConn // the role's own search_path
	store    dao.DataConn // WithSchema
	st       *pgstore.Store
	tenant   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, ctx: context.Background(), dsn: pgtest.ScratchURL(t), schema: "search_" + randHex(t), tenant: "lm-catalog"}
	f.business = f.open("business", "")
	f.exec(`CREATE EXTENSION vector SCHEMA public`)
	f.exec(`CREATE TABLE artist (id bigint PRIMARY KEY, name text NOT NULL, updated_at timestamptz NOT NULL)`)
	f.exec(`CREATE TABLE track (id bigint PRIMARY KEY, label_id text, title text, artist_id bigint NOT NULL REFERENCES artist,
		lyric text, updated_at timestamptz NOT NULL)`)
	f.exec(`INSERT INTO artist VALUES (1, 'Ann', $1)`, t0)
	f.store = f.open("store", "", pgstore.WithSchema(f.schema))
	if err := pgstore.EnsureSchema(f.ctx, f.store, f.schema); err != nil {
		t.Fatal(err)
	}
	if err := pgstore.Migrate(f.ctx, f.store); err != nil {
		t.Fatal(err)
	}
	st, err := pgstore.New(f.store, f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	f.st = st
	return f
}

// open is a pool on the fixture's database; query adds connection parameters to its DSN.
func (f *fixture) open(name, query string, opts ...postgres.Option) dao.DataConn {
	f.t.Helper()
	u, err := url.Parse(f.dsn)
	if err != nil {
		f.t.Fatal(err)
	}
	if query != "" {
		q := u.Query()
		extra, _ := url.ParseQuery(query)
		for k, v := range extra {
			q[k] = v
		}
		u.RawQuery = q.Encode()
	}
	conn, err := postgres.OpenNamed(f.ctx, name, u.String(), opts...)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { conn.Close() })
	return conn
}

func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.business.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

// track writes a track row: label "" is NULL.
func (f *fixture) track(id int, label, title string, sec int) {
	f.t.Helper()
	var l any
	if label != "" {
		l = label
	}
	f.exec(`INSERT INTO track (id, label_id, title, artist_id, lyric, updated_at) VALUES ($1, $2, $3, 1, $4, $5)
		ON CONFLICT (id) DO UPDATE SET label_id = $2, title = $3, lyric = $4, updated_at = $5`,
		id, l, title, "words of "+title, at(sec))
}

// source loads the sample view, with edit applied to its text.
func (f *fixture) source(edit func(string) string) *Source {
	f.t.Helper()
	src := trackView
	if edit != nil {
		src = edit(src)
	}
	s, err := Load(writeView(f.t, src), []string{"label_id"})
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// ingester is an ingester of src holding the tenant's lock, on a lock pool of its own, whose
// clock is now with no lag unless o says otherwise.
func (f *fixture) ingester(src *Source, o Options, query string) *Ingester {
	f.t.Helper()
	if o.Schema == "" {
		o.Schema = f.schema
	}
	if o.Now == nil {
		o.Now = func(context.Context) (time.Time, error) { return at(1_000_000), nil }
		if o.Lag == 0 {
			o.Lag = -1
		}
	}
	if o.LockPoll == 0 {
		o.LockPoll = 20 * time.Millisecond
	}
	in := New(f.st, f.tenant, src, f.business, f.open("locks", query), o)
	if err := in.Acquire(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(in.Release)
	return in
}

// docs are the tenant's documents.
func (f *fixture) docs() map[string]pgstore.DocInfo {
	f.t.Helper()
	out := map[string]pgstore.DocInfo{}
	page, _, err := f.st.Docs(f.ctx, "", 10000)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, d := range page {
		out[d.Path] = d
	}
	return out
}

func (f *fixture) paths() []string {
	d := f.docs()
	out := make([]string, 0, len(d))
	for p := range d {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// quarantined are the ids in quarantine, sorted.
func (f *fixture) quarantined() []string {
	f.t.Helper()
	q, err := f.st.Quarantined(f.ctx, 10000)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, e := range q {
		out = append(out, e.SourceID)
	}
	sort.Strings(out)
	return out
}

// lexical are the paths a word finds.
func (f *fixture) lexical(word string) []string {
	f.t.Helper()
	var out []string
	err := f.st.View(f.ctx, func(v *pgstore.View) error {
		hits, err := v.Lexical(f.ctx, []search.Term{{Text: word}}, search.Filter{}, 100)
		for _, h := range hits {
			if !slices.Contains(out, h.Path) {
				out = append(out, h.Path)
			}
		}
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) scalar(conn dao.DataConn, q string, args ...any) string {
	f.t.Helper()
	rows, err := conn.QueryContext(f.ctx, q, args...)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var s string
	if !rows.Next() {
		f.t.Fatalf("%s: no row (%v)", q, rows.Err())
	}
	if err := rows.Scan(&s); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	return s
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func trackPaths(ids ...int) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = fmt.Sprintf("track/%d.md", id)
	}
	sort.Strings(out)
	return out
}

func randHex(t *testing.T) string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// failingConn fails the view's queries from the nth on: a source that goes away mid-scan.
type failingConn struct {
	dao.DataConn
	n, calls atomic.Int64
}

func (c *failingConn) QueryContext(ctx context.Context, q string, args ...any) (dao.Rows, error) {
	if strings.Contains(q, "jsonb_build_object") && c.calls.Add(1) >= c.n.Load() {
		return nil, fmt.Errorf("the source went away")
	}
	return c.DataConn.QueryContext(ctx, q, args...)
}
