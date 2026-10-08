package rpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/vfs/local"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// rawRig is a daemon whose workspace kb holds page.html and manual.pdf, derived as a build derives
// them (HTML by golib's extractor, the PDF by pdfMarkdown), and whose files outside every workspace
// are the disk's, derived the same way: served over network, its client at protocol proto.
func rawRig(t *testing.T, network string, proto int64) *golibrpc.Client {
	t.Helper()
	ctx := context.Background()
	reg, err := registrations.New(nil, registrations.Documents(derived.MaxContainer, derived.MaxText), pdfMarkdown{})
	if err != nil {
		t.Fatal(err)
	}
	opts := []docs.Option{docs.WithRegistrations(reg.Kinds()), docs.WithDeriver(reg)}
	fsys := memfs.New()
	for name, body := range map[string]string{"page.html": "<h1>Title</h1><p>body</p>\n", "manual.pdf": "%PDF ascii\n"} {
		if _, err := fsys.WriteFile(ctx, name, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	disk, err := local.New("/")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	addr := shortSocket(t)
	if network == "tcp" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Fixed(&Workspace{Name: "kb", Root: "/roots/kb", Docs: docs.New(fsys, nil, opts...)}), "v-test",
		WithListener(ln), WithFiles(docs.New(disk, nil, opts...)))
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- srv.Run(rctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop")
		}
	})
	cli, err := golibrpc.Dial(ctx, ln.Addr().String(), msgpackrpc.New(nil), golibrpc.ClientNetwork(network))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Call(ctx, "sys.hello", map[string]any{"protocol": proto, "name": "test"}); err != nil {
		t.Fatal(err)
	}
	return cli
}

// TestRawVerbsReadAndWriteAnHTMLFile: doc.read_raw gives a workspace's HTML file its own bytes
// where doc.read gives its derived text, and doc.write_raw saves new ones where doc.write refuses;
// file.read_raw and file.write_raw do the same outside every workspace. A PDF, ASCII though it is,
// is refused raw and left as it was.
func TestRawVerbsReadAndWriteAnHTMLFile(t *testing.T) {
	cli := rawRig(t, "unix", Protocol)
	plain := call(t, cli, "doc.read", "kb", "page.html").(map[string]any)
	if strings.Contains(string(plain["content"].([]byte)), "<h1>") {
		t.Fatalf("doc.read of an HTML file gave its bytes: %q", plain["content"])
	}
	raw := call(t, cli, "doc.read_raw", "kb", "page.html").(map[string]any)
	if string(raw["content"].([]byte)) != "<h1>Title</h1><p>body</p>\n" || raw["version"] != plain["version"] {
		t.Fatalf("doc.read_raw = %v, want the page's bytes at the file's version %v", raw, plain["version"])
	}
	if err := callErr(cli, "doc.write", "kb", "page.html", []byte("<p>x</p>"), raw["version"]); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("doc.write of an HTML file: %v, want refused", err)
	}
	v := call(t, cli, "doc.write_raw", "kb", "page.html", []byte("<p>edited</p>\n"), raw["version"]).(map[string]any)["version"]
	again := call(t, cli, "doc.read_raw", "kb", "page.html").(map[string]any)
	if string(again["content"].([]byte)) != "<p>edited</p>\n" || again["version"] != v {
		t.Errorf("after doc.write_raw: %v, want the edit at %v", again, v)
	}
	pdf := call(t, cli, "doc.read", "kb", "manual.pdf").(map[string]any)
	if err := callErr(cli, "doc.read_raw", "kb", "manual.pdf"); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("doc.read_raw of a PDF: %v, want refused", err)
	}
	if err := callErr(cli, "doc.write_raw", "kb", "manual.pdf", []byte("text"), pdf["version"]); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("doc.write_raw over a PDF: %v, want refused", err)
	}
	if got := call(t, cli, "doc.read", "kb", "manual.pdf").(map[string]any); got["version"] != pdf["version"] {
		t.Errorf("the PDF changed: %v", got)
	}

	abs := filepath.Join(t.TempDir(), "out.html")
	if err := os.WriteFile(abs, []byte("<p>outside</p>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fr := call(t, cli, "file.read_raw", abs).(map[string]any)
	if string(fr["content"].([]byte)) != "<p>outside</p>\n" {
		t.Fatalf("file.read_raw = %q", fr["content"])
	}
	if got := call(t, cli, "file.read", abs).(map[string]any); strings.Contains(string(got["content"].([]byte)), "<p>") {
		t.Errorf("file.read of an HTML file gave its bytes: %q", got["content"])
	}
	call(t, cli, "file.write_raw", abs, []byte("<p>saved</p>\n"), fr["version"])
	if b, _ := os.ReadFile(abs); string(b) != "<p>saved</p>\n" {
		t.Errorf("file.write_raw left %q", b)
	}
}

// TestRawVerbsNeedProtocol19AndALocalPeer: a session below 19 does not have the four verbs; over
// TCP, file.read_raw and file.write_raw are access denied, as file.read and file.write are.
func TestRawVerbsNeedProtocol19AndALocalPeer(t *testing.T) {
	old := rawRig(t, "unix", 18)
	for _, v := range []string{"doc.read_raw", "doc.write_raw", "file.read_raw", "file.write_raw"} {
		err := callErr(old, v, "kb", "page.html")
		if code(err) != golibrpc.CodeMethodNotFound || !strings.Contains(err.Error(), "needs protocol 19") {
			t.Errorf("%s at 18: %v, want an unknown method naming protocol 19", v, err)
		}
	}
	remote := rawRig(t, "tcp", Protocol)
	abs := filepath.Join(t.TempDir(), "a.html")
	if err := os.WriteFile(abs, []byte("<p>a</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]any{{"file.read_raw", abs}, {"file.write_raw", abs, []byte("x"), ""}} {
		if err := callErr(remote, c[0].(string), c[1:]...); code(err) != golibrpc.CodeAccessDenied {
			t.Errorf("%s over tcp: %v, want access denied", c[0], err)
		}
	}
	if b, _ := os.ReadFile(abs); string(b) != "<p>a</p>" {
		t.Errorf("file.write_raw over tcp wrote: %q", b)
	}
}
