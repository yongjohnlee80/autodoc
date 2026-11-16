//go:build server

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/internal/ipc"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// --copy-store-from copies a SQLite store into the config's store on PostgreSQL, and a daemon on
// that config then serves the copy's workspaces. It is refused while the file is held (its daemon
// serving it), and for a config whose store is SQLite; a destination with rows is refused before
// the file is opened, so the file is left as it was. The report names each table and ends with
// what the copy did not do. The mutant, named here: the file opened before the destination.
func TestCopyStoreFromFillsTheStoreADaemonThenServes(t *testing.T) {
	skipOnPipes(t)
	ctx := context.Background()
	dir := short(t)
	state := filepath.Join(dir, "sqlite")
	writeConfig(t, ipc.ScratchAddr(dir, "s.sock"), state, "", "kb="+t.TempDir(), "notes="+t.TempDir())
	file := filepath.Join(state, config.StoreName)

	t.Setenv("AUTODOC_TEST_COPY_DSN", pgtest.ScratchURL(t))
	sock := ipc.ScratchAddr(dir, "p.sock")
	pg := "[store]\nengine = \"postgres\"\ndsn_env = \"AUTODOC_TEST_COPY_DSN\"\nschema = \"autodoc_copy\"\n"
	cfg := writeConfig(t, sock, filepath.Join(dir, "pg"), pg)

	if err := runCopyStore(ctx, writeConfig(t, sock, filepath.Join(dir, "x"), ""), file, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), `engine = "postgres"`) {
		t.Errorf("a copy into a SQLite store's config: %v", err)
	}
	held, err := store.Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if err := runCopyStore(ctx, cfg, file, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "stop it first") {
		t.Errorf("a copy of a file a daemon holds: %v", err)
	}
	_ = held.Close()

	var out bytes.Buffer
	if err := runCopyStore(ctx, cfg, file, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"into the PostgreSQL store in schema autodoc_copy", "workspace", "2 rows", "not done by the copy", "no daemon was started"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report does not say %q:\n%s", want, out.String())
		}
	}

	// a second copy into the now-full destination is refused for it before the file is opened:
	// the file held as a daemon would hold it, the refusal is still the destination's
	held, err = store.Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if err := runCopyStore(ctx, cfg, file, &bytes.Buffer{}); !errors.Is(err, store.ErrCopyNotEmpty) {
		t.Errorf("a copy into a destination with rows, its file held: %v, want ErrCopyNotEmpty before the file is opened", err)
	}
	_ = held.Close()

	start(t, cfg, sock)
	cli := dial(t, sock)
	call(t, cli, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": "test"})
	if got := fmt.Sprint(call(t, cli, "workspace.list")); !strings.Contains(got, "kb") || !strings.Contains(got, "notes") {
		t.Errorf("the daemon on the copy lists %s, want kb and notes", got)
	}
}
