package todo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/vfs/local"
)

// TestParityWithAStoreCopy is opt-in: AUTODOC_TODO_PARITY_DIR names a COPY of a real store (a
// .todo-list directory), never the live one. Every task file is decoded and rendered again: a
// file auto-core wrote comes back byte for byte. A file that does not (a hand edit auto-core would
// rewrite too) is printed, not failed, unless AUTODOC_TODO_PARITY_LUA_DIR holds auto-core's own
// rendering of each file (md.encode(md.decode(f)), same relative paths) and Go's differs from it.
// The copy is read through golib's vfs/local only.
func TestParityWithAStoreCopy(t *testing.T) {
	dir := os.Getenv("AUTODOC_TODO_PARITY_DIR")
	if dir == "" {
		t.Skip("AUTODOC_TODO_PARITY_DIR is not set")
	}
	luaDir := os.Getenv("AUTODOC_TODO_PARITY_LUA_DIR")
	fsys, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	var lua *local.FS
	if luaDir != "" {
		if lua, err = local.New(luaDir); err != nil {
			t.Fatal(err)
		}
		defer lua.Close()
	}
	ctx := context.Background()
	type count struct{ files, same, rewritten, malformed int }
	counts := map[string]*count{"active": {}, "archived": {}}
	read := func(fs *local.FS, p string) ([]byte, error) {
		r, err := fs.Open(ctx, p, 0)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	}
	var walk func(d string)
	walk = func(d string) {
		entries, err := fsys.ReadDir(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			switch {
			case strings.HasPrefix(e.Name, "."):
			case e.IsDir():
				walk(e.Path)
			case strings.HasSuffix(e.Name, ".md"):
				c := counts["active"]
				if strings.HasPrefix(e.Path, "archived/") {
					c = counts["archived"]
				}
				c.files++
				src, err := read(fsys, e.Path)
				if err != nil {
					t.Fatal(err)
				}
				task, err := decode(src)
				if err != nil {
					c.malformed++
					t.Logf("MALFORMED %s: %v", e.Path, err)
					continue
				}
				got := encode(task)
				if bytes.Equal(got, src) {
					c.same++
					continue
				}
				c.rewritten++
				if lua == nil {
					t.Logf("REWRITTEN (a hand edit auto-core would rewrite too) %s", e.Path)
					continue
				}
				want, err := read(lua, e.Path)
				switch {
				case err != nil:
					t.Errorf("%s: no Lua rendering: %v", e.Path, err)
				case !bytes.Equal(got, want):
					t.Errorf("%s: Go and auto-core render it differently", e.Path)
				default:
					t.Logf("REWRITTEN by auto-core too (a hand edit) %s", e.Path)
				}
			}
		}
	}
	walk(".")
	for _, k := range []string{"active", "archived"} {
		c := counts[k]
		t.Logf("%-8s files %d: byte-identical %d, rewritten as auto-core rewrites them %d, malformed %d", k, c.files, c.same, c.rewritten, c.malformed)
	}
	if counts["active"].files+counts["archived"].files == 0 {
		t.Fatal(errors.New("no task files: is AUTODOC_TODO_PARITY_DIR a .todo-list?"))
	}
}
