package rpc

import (
	"context"
	"reflect"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// singles is a set of workspaces with the single-field setters, recording what each was asked.
type singles struct {
	Workspaces
	got []any
	err error
}

func (s *singles) SetPatterns(_ context.Context, name string, include, exclude []string) error {
	s.got = append(s.got, "patterns", name, include, exclude)
	return s.err
}
func (s *singles) SetProvider(_ context.Context, name, provider string) error {
	s.got = append(s.got, "provider", name, provider)
	return s.err
}
func (s *singles) SetTextExtensions(_ context.Context, name string, exts []string) ([]string, error) {
	s.got = append(s.got, "texts", name, exts)
	return []string{".log"}, s.err
}
func (s *singles) SetSchema(_ context.Context, name, path string) (SchemaStatus, error) {
	s.got = append(s.got, "schema", name, path)
	return SchemaStatus{Path: path, Active: true, Fields: 2}, s.err
}
func (s *singles) SetSectionTokens(_ context.Context, name string, tokens int) error {
	s.got = append(s.got, "section", name, tokens)
	return s.err
}

// The single-field verbs, kept for scripts and older clients beside workspace.configure: each reads
// its parameters, refuses a wrong shape, reaches the manager, and passes its refusal on.
func TestTheSingleFieldVerbs(t *testing.T) {
	m := &singles{Workspaces: Fixed()}
	cli := dialServer(t, m)
	ctx := context.Background()
	call(t, cli, "workspace.set_patterns", "kb", []any{"**/*.md"}, []any{".git/**"})
	call(t, cli, "workspace.set_provider", "kb", "local")
	if got := call(t, cli, "workspace.set_text_extensions", "kb", []any{"log"}); !reflect.DeepEqual(got, []any{".log"}) {
		t.Errorf("set_text_extensions answered %v, want the normalized extensions", got)
	}
	if got, _ := call(t, cli, "workspace.set_schema", "kb", "s.yaml").(map[string]any); got["path"] != "s.yaml" || got["active"] != true {
		t.Errorf("set_schema answered %v, want the schema's status", got)
	}
	call(t, cli, "workspace.section_size", "kb", 256)
	want := []any{"patterns", "kb", []string{"**/*.md"}, []string{".git/**"}, "provider", "kb", "local",
		"texts", "kb", []string{"log"}, "schema", "kb", "s.yaml", "section", "kb", 256}
	if !reflect.DeepEqual(m.got, want) {
		t.Errorf("the manager was asked\n %v\nwant %v", m.got, want)
	}

	for name, params := range map[string][]any{
		"workspace.set_patterns":        {"kb", "**/*.md", []any{}},
		"workspace.set_provider":        {"kb", 7},
		"workspace.set_text_extensions": {"kb", "log"},
		"workspace.set_schema":          {"kb", 7},
		"workspace.section_size":        {"kb", "big"},
	} {
		if _, err := cli.Call(ctx, name, params...); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("%s with a wrong shape: %v, want InvalidParams", name, err)
		}
	}
	if _, err := cli.Call(ctx, "workspace.set_patterns", "kb", []any{}, "x"); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("set_patterns with an exclude not a list: %v", err)
	}
	for name, params := range map[string][]any{
		"workspace.set_patterns":        {1, []any{}, []any{}},
		"workspace.set_provider":        {1, ""},
		"workspace.set_text_extensions": {1, []any{}},
		"workspace.set_schema":          {1, ""},
		"workspace.section_size":        {1, 256},
	} {
		if _, err := cli.Call(ctx, name, params...); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("%s without a workspace name: %v, want InvalidParams", name, err)
		}
	}

	m.err = store.ErrNoWorkspace
	for name, params := range map[string][]any{
		"workspace.set_patterns":        {"gone", []any{}, []any{}},
		"workspace.set_provider":        {"gone", ""},
		"workspace.set_text_extensions": {"gone", []any{}},
		"workspace.set_schema":          {"gone", ""},
		"workspace.section_size":        {"gone", 256},
	} {
		if _, err := cli.Call(ctx, name, params...); code(err) != CodeNoSuchWorkspace {
			t.Errorf("%s of a workspace gone: %v, want no such workspace", name, err)
		}
	}
	fixed := dialServer(t, Fixed())
	for name, params := range map[string][]any{
		"workspace.set_patterns":        {"kb", []any{}, []any{}},
		"workspace.set_provider":        {"kb", ""},
		"workspace.set_text_extensions": {"kb", []any{}},
		"workspace.set_schema":          {"kb", ""},
		"workspace.section_size":        {"kb", 256},
	} {
		if _, err := fixed.Call(ctx, name, params...); code(err) != CodeUnsupported {
			t.Errorf("%s on a fixed set: %v, want unsupported", name, err)
		}
	}
}
