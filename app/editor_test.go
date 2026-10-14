package app

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// TestEditorCallsThePageAsTheAgent: --editor dials $AUTODOC_EDITOR and passes $AUTODOC_EDITOR_TOKEN first,
// then the params; a refusal comes back as the CallError --call prints; without the environment it
// says what is missing.
func TestEditorCallsThePageAsTheAgent(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "e.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("editor.echo", func(_ context.Context, req *golibrpc.Request) (any, error) { return req.Params, nil })
	srv.Handle("editor.no", func(context.Context, *golibrpc.Request) (any, error) {
		return nil, &golibrpc.Error{Code: -32001, Message: "needs editor.read: not granted"}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	env := func(k string) string {
		return map[string]string{"AUTODOC_EDITOR": sock, "AUTODOC_EDITOR_TOKEN": "tok"}[k]
	}
	var out bytes.Buffer
	if err := runEditor(ctx, "editor.echo", `[3, "x"]`, env, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(strings.Fields(out.String()), ""); got != `["tok",3,"x"]` {
		t.Fatalf("echo: %s", out.String())
	}
	err = runEditor(ctx, "editor.no", "", env, &out)
	if ce, ok := err.(*CallError); !ok || ce.Code != -32001 || !strings.Contains(ce.Message, "editor.read") {
		t.Fatalf("a refusal: %v", err)
	}
	if err := runEditor(ctx, "editor.echo", "", func(string) string { return "" }, &out); err == nil ||
		!strings.Contains(err.Error(), "$AUTODOC_EDITOR and $AUTODOC_EDITOR_TOKEN are not set") {
		t.Fatalf("no environment: %v", err)
	}
}
