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

// TestTheWorkspaceNoticePrintsALineOrNothing (docs/decisions/crew.md#the-workspace-notice-hook):
// --workspace-notice prints nothing when editor.workspace has no switch, the switch's notice as one
// line when it has one, and nothing with the editor refusing, gone or never set; it always exits 0,
// as a hook must.
func TestTheWorkspaceNoticePrintsALineOrNothing(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "e.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	answers := []any{
		map[string]any{"workspace_in_use": map[string]any{"name": "kb"}},
		map[string]any{"switch": map[string]any{"kind": "entered", "notice": "[AutoDoc] the editor moved to workspace notes"}},
		&golibrpc.Error{Code: -32001, Message: "refused"},
	}
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("editor.workspace", func(_ context.Context, req *golibrpc.Request) (any, error) {
		if len(req.Params) != 1 || req.Params[0] != "tok" {
			t.Errorf("editor.workspace was called with %v, want the token alone", req.Params)
		}
		if len(answers) == 0 { // still answering as it stops: as a refusal would
			return nil, &golibrpc.Error{Code: -32001, Message: "stopping"}
		}
		a := answers[0]
		answers = answers[1:]
		if err, ok := a.(*golibrpc.Error); ok {
			return nil, err
		}
		return a, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Run(ctx) }()
	env := func(k string) string {
		return map[string]string{"AUTODOC_EDITOR": sock, "AUTODOC_EDITOR_TOKEN": "tok"}[k]
	}
	for i, want := range []string{"", "[AutoDoc] the editor moved to workspace notes\n", ""} {
		var out bytes.Buffer
		if code := workspaceNotice(ctx, env, &out); code != 0 || out.String() != want {
			t.Errorf("answer %d: exit %d, printed %q, want 0 and %q", i, code, out.String(), want)
		}
	}
	cancel() // the editor gone
	for name, env := range map[string]func(string) string{"gone": env, "unset": func(string) string { return "" }} {
		var out bytes.Buffer
		if code := workspaceNotice(context.Background(), env, &out); code != 0 || out.Len() != 0 {
			t.Errorf("%s: exit %d, printed %q, want 0 and nothing", name, code, out.String())
		}
	}
}
