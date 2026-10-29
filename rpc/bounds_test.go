package rpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/fsbounds"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// TestAnUndeclaredVerbIsBoundedToo: enforcement is where the files are reached, so a verb that
// declares nothing about paths, reading through a workspace's guarded documents with its request's
// ctx, is refused a hidden file under bounds, and reads it for the user (ADR 1792004705 r11 F1's
// audit).
func TestAnUndeclaredVerbIsBoundedToo(t *testing.T) {
	root := t.TempDir()
	for p, s := range map[string]string{"a.md": "# A\n", ".hidden/n.md": "# N\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	disk, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	g := fsbounds.NewGuard()
	g.SetServed(func() []fsbounds.Root { return []fsbounds.Root{{Name: "kb", Dir: root}} })
	d := docs.New(g.Wrap(func() string { return "kb" }, root, disk), nil)
	type peek struct {
		Path string `param:"path"`
	}
	probe := module.Handle(module.Contract{Verb: "todos.peek", Since: 12, Effect: module.EffectRead},
		func(ctx context.Context, p peek) (string, error) {
			doc, err := d.Read(ctx, p.Path)
			return string(doc.Content), err
		})
	cli, _ := moduleServer(t, fakeModule{name: "todos", verbs: []module.Verb{probe}})
	ctx := context.Background()
	if got, err := cli.Call(ctx, "todos.peek", "a.md", map[string]any{"within": "*"}); err != nil || got != "# A\n" {
		t.Errorf("a visible file, bounded: %v, %v", got, err)
	}
	_, err = cli.Call(ctx, "todos.peek", ".hidden/n.md", map[string]any{"within": "*"})
	var re *golibrpc.Error
	if !errors.As(err, &re) || re.Code != golibrpc.CodeAccessDenied || !strings.Contains(re.Message, "is hidden") {
		t.Errorf("a hidden file, bounded, through a verb that declares nothing: %v, want access denied", err)
	}
	if got, err := cli.Call(ctx, "todos.peek", ".hidden/n.md"); err != nil || got != "# N\n" {
		t.Errorf("the user's call of it: %v, %v", got, err)
	}
	if _, err := cli.Call(ctx, "todos.peek", "a.md", map[string]any{"protect": []any{}}); err == nil {
		t.Errorf("bounds without within were taken")
	}
}

// directDisk is a call that reaches the disk itself, past every workspace's guarded filesystem.
var directDisk = regexp.MustCompile(`\bos\.(ReadFile|WriteFile|Open|OpenFile|Create|Remove|RemoveAll|Rename|Mkdir|MkdirAll|Symlink|Link|Chmod|Truncate)\(`)

// directDiskAllowed are the calls that reach the disk for no request, each with why.
var directDiskAllowed = map[string]string{
	"internal/daemon/schema.go": "readSchema: the schema holder's own poll of the file a workspace points at, never a request's path",
}

// TestHandlersReachFilesThroughTheGuard: a text assertion over rpc's and the daemon's sources. A
// gated script's call is bounded where a workspace's files are reached (core/fsbounds); a handler
// that went to the disk itself (os.*) would be a route the guard never sees, so each one there is
// named here with why it serves no request (ADR 1792004705 §4.2a.1).
func TestHandlersReachFilesThroughTheGuard(t *testing.T) {
	for _, dir := range []string{".", "../internal/daemon"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			rel := filepath.ToSlash(filepath.Join("rpc", f))
			if dir != "." {
				rel = strings.TrimPrefix(filepath.ToSlash(f), "../")
			}
			for i, line := range strings.Split(string(src), "\n") {
				if directDisk.MatchString(line) && directDiskAllowed[rel] == "" {
					t.Errorf("%s:%d reaches the disk itself, past the guard: %s", rel, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
