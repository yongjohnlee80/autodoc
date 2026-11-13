package rpc

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/internal/ipc"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/memfs"
)

// refusingFS is a disk the system refuses every file of: each operation answers fs.ErrPermission
// as the operating system's error does, an *fs.PathError.
type refusingFS struct{ *memfs.FS }

func refusedAt(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrPermission}
}

func (refusingFS) Stat(_ context.Context, n string) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, refusedAt("stat", n)
}
func (refusingFS) ReadDir(_ context.Context, n string) ([]vfs.FileInfo, error) {
	return nil, refusedAt("readdir", n)
}
func (refusingFS) Open(_ context.Context, n string, _ int64) (io.ReadCloser, error) {
	return nil, refusedAt("open", n)
}
func (refusingFS) WriteFile(_ context.Context, n string, _ io.Reader, _ ...vfs.WriteOption) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, refusedAt("write", n)
}
func (refusingFS) WriteFileIf(_ context.Context, n string, _ io.Reader, _ vfs.Version, _ ...vfs.WriteOption) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, refusedAt("write", n)
}
func (refusingFS) CreateExclusive(_ context.Context, n string, _ io.Reader, _ ...vfs.WriteOption) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, refusedAt("create", n)
}
func (refusingFS) MkdirAll(_ context.Context, n string) error  { return refusedAt("mkdir", n) }
func (refusingFS) Remove(_ context.Context, n string) error    { return refusedAt("remove", n) }
func (refusingFS) RemoveAll(_ context.Context, n string) error { return refusedAt("remove", n) }
func (refusingFS) RemoveIf(_ context.Context, n string, _ vfs.Version) error {
	return refusedAt("remove", n)
}
func (refusingFS) Rename(_ context.Context, from, _ string) error { return refusedAt("rename", from) }
func (refusingFS) RenameNoReplace(_ context.Context, from, _ string) error {
	return refusedAt("rename", from)
}

// TestEveryVerbThatAnswersAFileRefusalListsIt: the catalog promises every code a verb may answer
// (contracts.go). Each verb whose handler hands the system's errors back, called through its real
// handler on a file the system refuses (a workspace's, or one outside every workspace), answers
// rpc.file_refused, and its contract lists it; and they are every verb that lists it. A listing
// dropped makes that verb answer an internal error (unlistedRefusal); a listing added without a
// row here fails the count.
func TestEveryVerbThatAnswersAFileRefusalListsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	disks := NewDisks(func(string) (*docs.Docs, func(), error) {
		return docs.New(refusingFS{memfs.New()}, nil), func() {}, nil
	})
	t.Cleanup(disks.Close)
	sock := shortSocket(t)
	ln, err := ipc.Listen(sock, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	ws := Static(WorkspaceInfo{Name: "kb", Root: filepath.Join(t.TempDir(), "kb"), Docs: docs.New(refusingFS{memfs.New()}, md)})
	srv := New(Fixed(ws), "v-test", WithListener(ln), WithDisks(disks))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	cli, err := golibrpc.Dial(ctx, sock, msgpackrpc.New(nil), golibrpc.ClientNetwork(ipc.Network), golibrpc.WithConnDialer(ipc.RPCDialer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	call(t, cli, "sys.hello", map[string]any{"protocol": Protocol, "name": "test"})

	outside := filepath.Join(t.TempDir(), "outside.md") // absolute and clean, in no workspace
	text := []byte("a\n")
	calls := map[string][]any{
		"doc.read":       {"kb", "a.md"},
		"doc.read_raw":   {"kb", "a.md"},
		"doc.outline":    {"kb", "a.md"},
		"doc.write":      {"kb", "a.md", text, ""},
		"doc.write_raw":  {"kb", "a.md", text, ""},
		"doc.rename":     {"kb", "a.md", "b.md"},
		"doc.remove":     {"kb", "a.md", "v1"},
		"file.read":      {outside},
		"file.read_raw":  {outside},
		"file.write":     {outside, text, ""},
		"file.write_raw": {outside, text, ""},
	}
	var listing []string
	for _, c := range srv.Catalog(Protocol, "").Verbs {
		if slices.Contains(c.Errors, CodeFileRefused) {
			listing = append(listing, c.Verb)
		}
	}
	var driven []string
	for verb := range calls {
		driven = append(driven, verb)
	}
	sort.Strings(listing)
	sort.Strings(driven)
	if !slices.Equal(listing, driven) {
		t.Errorf("the verbs listing rpc.file_refused are %v, the ones driven here %v: each verb that lists it has a row, and each row's verb lists it", listing, driven)
	}
	for _, verb := range driven {
		_, err := cli.Call(ctx, verb, calls[verb]...)
		if code(err) != CodeFileRefused {
			t.Errorf("%s on a file the system refuses answered %v, want rpc.file_refused (%d)", verb, err, CodeFileRefused)
		}
	}
}

// TestAVerbThatDoesNotListAFileRefusalAnswersAnInternalError: a verb whose contract doesn't list
// rpc.file_refused answers the system's refusal as an unmapped error (an internal error, its detail
// in the log), so it never answers a code its contract doesn't promise; one that lists it answers
// it, and any other error is left as it was.
func TestAVerbThatDoesNotListAFileRefusalAnswersAnInternalError(t *testing.T) {
	refusal := refusedAt("open", "/somewhere/a.md")
	var re *golibrpc.Error
	if errors.As(wireErr(unlistedRefusal(refusal, false)), &re) {
		t.Fatalf("an unlisted verb's refusal answered %d %q, want an internal error", re.Code, re.Message)
	}
	if got := unlistedRefusal(refusal, false); got.Error() != refusal.Error() {
		t.Fatalf("the internal error says %q, want its detail %q for the log", got, refusal)
	}
	if !errors.As(wireErr(unlistedRefusal(refusal, true)), &re) || re.Code != CodeFileRefused {
		t.Fatalf("a listing verb's refusal answered %v, want rpc.file_refused", wireErr(unlistedRefusal(refusal, true)))
	}
	for _, other := range []error{ErrNoPull, errors.New("something else")} {
		if got := unlistedRefusal(other, false); got != other {
			t.Fatalf("an unlisted verb's %v became %v", other, got)
		}
	}
}
