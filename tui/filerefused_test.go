package tui

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// TestAnOutsideSaveTheSystemRefusesSaysWhy: a save of a file outside every workspace that the
// daemon answers rpc.file_refused (a file another program holds, or one not permitted) says the
// daemon's words on the status line. It never says this AutoDoc is remote, which is how an access
// denied answer to file.* reads.
func TestAnOutsideSaveTheSystemRefusesSaysWhy(t *testing.T) {
	t.Parallel()
	const why = "the file is in use by another program: try again in a moment"
	notes := filepath.Join(fileDir(t, "notes.md", "# Notes\n"), "notes.md")
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	var armed atomic.Bool
	sess := NewSession(d.sock, nil)
	sess.SetCallError(func(method string) error {
		if method == "file.write" && armed.CompareAndSwap(true, false) {
			return &golibrpc.Error{Code: rpc.CodeFileRefused, Message: why}
		}
		return nil
	})
	r := runTUI(t, sess, Options{})
	r.waitAttached(t, "kb")
	r.h.p.Post(func() { r.h.openAbsolute(notes) })
	r.waitOpen(t, "", notes)
	r.typeInEditor(t, "more ")
	armed.Store(true)
	r.keys(t, decltest.Ctrl('s'))
	r.waitStatus(t, why)
	if s := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw }); strings.Contains(s, "remote") {
		t.Fatalf("the status says %q: a file the system refused read as a remote daemon", s)
	}
}
