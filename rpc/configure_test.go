package rpc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/edition"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/store"
)

func ptr[T any](v T) *T { return &v }

// Every setting the TUI sends is read into its change, and a key it leaves out stays nil.
func TestChangesOf_ReadsEverySetting(t *testing.T) {
	c, err := changesOf(map[string]any{
		"name":                   "docs",
		"include":                []any{"**/*.md"},
		"exclude":                []any{},
		"schema":                 "schema.yaml",
		"text_extensions":        []any{".org"},
		"section_tokens":         int64(256),
		"embedding_policy":       "never",
		"provider":               "",
		"destination":            "postgres",
		"vector_index":           "hnsw",
		"view_args":              map[string]any{"LabelGroupID": "7"},
		"source":                 map[string]any{"engine": "postgres", "dsn": "postgres://db/src", "schema": "labels"},
		"destination_connection": map[string]any{"remove": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := store.Changes{
		Name:            ptr("docs"),
		Include:         ptr([]string{"**/*.md"}),
		Exclude:         ptr([]string{}),
		SchemaPath:      ptr("schema.yaml"),
		TextExtensions:  ptr([]string{".org"}),
		SectionTokens:   ptr(256),
		EmbeddingPolicy: ptr("never"),
		Provider:        ptr(""),
		Destination:     ptr("postgres"),
		VectorIndex:     ptr("hnsw"),
		ViewArgs:        ptr(map[string]any{"LabelGroupID": "7"}),
		Source:          &store.ConnectionSpec{Engine: "postgres", DSN: "postgres://db/src", Schema: "labels"},
		DestinationConn: &store.ConnectionSpec{Remove: true},
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("changes\n got %+v\nwant %+v", c, want)
	}
	if c, err := changesOf(map[string]any{"section_tokens": uint64(0)}); err != nil || !reflect.DeepEqual(c, store.Changes{SectionTokens: ptr(0)}) {
		t.Errorf("one setting: %+v, %v; want only it set", c, err)
	}
}

// A key it does not know, or a value of the wrong shape, is InvalidParams, at the top and inside a
// connection: never dropped.
func TestChangesOf_RefusesWhatItDoesNotRead(t *testing.T) {
	for name, m := range map[string]map[string]any{
		"an unknown key":                 {"name": "docs", "nmae": "docs"},
		"a name that is not a string":    {"name": int64(1)},
		"a pattern that is not a list":   {"include": "**/*.md", "exclude": []any{}},
		"a pattern that is not text":     {"include": []any{int64(1)}, "exclude": []any{}},
		"a section that is not a count":  {"section_tokens": "256"},
		"view args that are not a map":   {"view_args": []any{"a"}},
		"a connection that is not a map": {"source": "postgres://db/src"},
		"an unknown connection key":      {"source": map[string]any{"engine": "postgres", "password": "x"}},
		"a connection dsn not a string":  {"destination_connection": map[string]any{"dsn": int64(5)}},
		"a remove that is not a boolean": {"source": map[string]any{"remove": "yes"}},
	} {
		if _, err := changesOf(m); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("%s: %v, want InvalidParams", name, err)
		}
	}
}

// configurer is a set of workspaces that offers the database features and answers Configure with
// err, recording what it was asked.
type configurer struct {
	Workspaces
	err  error
	name string
	got  store.Changes
}

func (c *configurer) Capabilities() map[string]bool { return map[string]bool{"databases": true} }
func (c *configurer) Configure(_ context.Context, name string, ch store.Changes) error {
	c.name, c.got = name, ch
	return c.err
}

func dialServer(t *testing.T, ws Workspaces) *golibrpc.Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(ws, "v-test", WithListener(ln))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	cli, err := golibrpc.Dial(context.Background(), sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": Protocol, "name": "test"}); err != nil {
		t.Fatal(err)
	}
	return cli
}

func wireError(err error) (int64, string) {
	var e *golibrpc.Error
	if errors.As(err, &e) {
		return e.Code, e.Message
	}
	return 0, ""
}

// capabilities is sys.capabilities without its verbs, which it checks: a session at this build's
// protocol may call every verb.
func capabilities(t *testing.T, cli *golibrpc.Client) map[string]any {
	t.Helper()
	got := call(t, cli, "sys.capabilities").(map[string]any)
	verbs, _ := got["verbs"].([]any)
	if len(verbs) != len(New(Fixed(), "v").Verbs()) {
		t.Errorf("sys.capabilities' verbs %v: a session at protocol %d may call every verb", verbs, Protocol)
	}
	delete(got, "verbs")
	return got
}

// A fixed set of workspaces offers nothing beyond the core and configures nothing.
func TestConfigureVerbs_AFixedSet(t *testing.T) {
	cli := dialServer(t, Fixed())
	if got := capabilities(t, cli); !reflect.DeepEqual(got, map[string]any{"databases": false, "registrations": RegistrationsMap(registrations.Tables{}),
		"ranker": map[string]any{"supplied": false}}) {
		t.Errorf("sys.capabilities = %v, want databases false, no registrations and no ranker supplied", got)
	}
	_, err := cli.Call(context.Background(), "workspace.configure", "kb", map[string]any{"name": "docs"})
	if c, msg := wireError(err); c != CodeUnsupported || msg != "this server's set of workspaces is fixed" {
		t.Errorf("configure on a fixed set: %d %q", c, msg)
	}
}

// Over the wire: the settings reach the manager as changes, and what it refuses reaches the
// client as a code and a message that says why: a setting's own reason, the edition, or a name
// another workspace has.
func TestConfigureVerbs_OverTheWire(t *testing.T) {
	m := &configurer{Workspaces: Fixed()}
	cli := dialServer(t, m)
	if got := capabilities(t, cli); !reflect.DeepEqual(got, map[string]any{"databases": true, "registrations": RegistrationsMap(registrations.Tables{}),
		"ranker": map[string]any{"supplied": false}}) {
		t.Errorf("sys.capabilities = %v, want databases true, no registrations and no ranker supplied", got)
	}
	call(t, cli, "workspace.configure", "kb", map[string]any{"section_tokens": 256, "destination": "sqlite"})
	if m.name != "kb" || !reflect.DeepEqual(m.got, store.Changes{SectionTokens: ptr(256), Destination: ptr("sqlite")}) {
		t.Errorf("the manager got %q %+v", m.name, m.got)
	}
	call(t, cli, "workspace.configure", "kb", map[string]any{"abstract_chunk": true, "demote_superseded": false})
	if !reflect.DeepEqual(m.got, store.Changes{AbstractChunk: ptr(true), DemoteSuperseded: ptr(false)}) {
		t.Errorf("the retrieval settings reached the manager as %+v", m.got)
	}

	for _, tc := range []struct {
		name string
		err  error
		code int64
		msg  string
	}{
		{"a setting", &store.SettingError{Reason: "a postgres destination needs its connection"}, golibrpc.CodeInvalidParams, "a postgres destination needs its connection"},
		{"the edition", edition.ErrDatabases, CodeUnsupported, "this edition has no database settings"},
		{"a taken name", store.ErrTaken, CodeConflict, "another workspace has this name or root"},
	} {
		m.err = tc.err
		_, err := cli.Call(context.Background(), "workspace.configure", "kb", map[string]any{"name": "docs"})
		if c, msg := wireError(err); c != tc.code || msg != tc.msg {
			t.Errorf("%s: %d %q, want %d %q", tc.name, c, msg, tc.code, tc.msg)
		}
	}

	m.err, m.name = nil, ""
	for name, params := range map[string][]any{
		"an unknown setting":   {"kb", map[string]any{"colour": "red"}},
		"settings not a map":   {"kb", []any{"name"}},
		"no settings":          {"kb"},
		"a name not a string":  {int64(1), map[string]any{}},
		"a setting not a bool": {"kb", map[string]any{"abstract_chunk": "yes"}},
	} {
		if _, err := cli.Call(context.Background(), "workspace.configure", params...); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("%s: %v, want InvalidParams", name, err)
		}
	}
	if m.name != "" {
		t.Errorf("a refused call reached the manager for %q", m.name)
	}
}

// workspace.list reports a workspace's databases as where each connection points.
func TestDatabasesMap(t *testing.T) {
	got := databasesMap(Databases{UID: "u1", Destination: "postgres", VectorIndex: "hnsw",
		ViewArgs: map[string]any{"a": "b"},
		Connections: []store.ConnectionSummary{
			{Role: store.RoleSource, Engine: "postgres", Host: "src:5432", Database: "labels", User: "ro", HasPassword: true},
			{Role: store.RoleDestination, Engine: "postgres", Host: "db", Database: "rag", Schema: "autodoc"},
		}})
	want := map[string]any{"uid": "u1", "destination": "postgres", "vector_index": "hnsw", "view_args": map[string]any{"a": "b"},
		"source":                 map[string]any{"engine": "postgres", "host": "src:5432", "database": "labels", "user": "ro", "schema": "", "has_password": true},
		"destination_connection": map[string]any{"engine": "postgres", "host": "db", "database": "rag", "user": "", "schema": "autodoc", "has_password": false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("databases\n got %v\nwant %v", got, want)
	}
}

// recordedEvents is an event log that keeps what is appended.
type recordedEvents struct {
	mu  sync.Mutex
	got []store.Event
}

func (r *recordedEvents) AppendEvent(_ context.Context, e store.Event) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, e)
	return int64(len(r.got)), nil
}

func (r *recordedEvents) Events(context.Context, int64, int) ([]store.Event, int64, bool, error) {
	return nil, 0, false, nil
}

func (r *recordedEvents) all() []store.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]store.Event(nil), r.got...)
}

// A save logs what each of its changes' own verbs logs, so a client following the log does what it
// does for that verb: the rename first, with the new name, then the rest under it; the database
// settings as one event whose detail holds no connection. A refused save logs nothing.
func TestConfigureVerbs_LogAnEventForEachChange(t *testing.T) {
	m := &configurer{Workspaces: Fixed()}
	ev := &recordedEvents{}
	ctx, cancel := context.WithCancel(context.Background())
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(m, "v-test", WithListener(ln), WithEvents(ev))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	cli, err := golibrpc.Dial(context.Background(), sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": Protocol, "name": "tui"}); err != nil {
		t.Fatal(err)
	}

	call(t, cli, "workspace.configure", "kb", map[string]any{
		"name": "docs", "include": []any{"**/*.md"}, "exclude": []any{}, "embedding_policy": "never", "section_tokens": 256,
		"destination": "postgres", "destination_connection": map[string]any{"engine": "postgres", "dsn": "postgres://me:hunter2@db/rag"},
		"demote_superseded": true,
	})
	var got []string
	for _, e := range ev.all() {
		got = append(got, e.Kind+" "+e.Workspace+" "+e.Detail)
		if !strings.HasPrefix(e.Client, "tui#") {
			t.Errorf("%s: client %q, want the caller's token", e.Kind, e.Client)
		}
		if strings.Contains(e.Detail, "hunter2") || strings.Contains(e.Detail, "postgres://") {
			t.Errorf("%s: the log holds a connection: %q", e.Kind, e.Detail)
		}
	}
	want := []string{"workspace.renamed kb docs", "workspace.patterns docs ", "workspace.section_size docs ",
		"workspace.embedding_policy docs never", "workspace.databases docs ", "workspace.retrieval docs "}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events\n got %q\nwant %q", got, want)
	}

	m.err = store.ErrTaken
	before := len(ev.all())
	if _, err := cli.Call(context.Background(), "workspace.configure", "kb", map[string]any{"name": "other"}); err == nil {
		t.Fatal("a refused save succeeded")
	}
	if n := len(ev.all()); n != before {
		t.Errorf("a refused save logged %d events", n-before)
	}
}

// shortSocket is a socket path short enough for macOS (104 bytes): a test's TempDir, named after
// the test, is too long there.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}
