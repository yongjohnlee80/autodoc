package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
)

// runCopyStore is --copy-store-from (docs/decisions/store.md#copying-a-sqlite-store-into-postgresql):
// it copies the SQLite store at from into the config's store on PostgreSQL, which must be empty,
// in one transaction. Both stores are held while it runs, so neither may have a daemon serving it.
// Opening the file applies this build's scripts to it, as any start of this build would; they are
// said before anything is copied.
func runCopyStore(ctx context.Context, configPath, from string, out io.Writer) error {
	e, err := endpoint.Load(configPath)
	if err != nil {
		return err
	}
	if !e.Config.Store.Postgres() {
		return errors.New(`--copy-store-from copies into a store on PostgreSQL: set [store] engine = "postgres" in the config (the server build)`)
	}
	if fi, err := os.Stat(from); err != nil || fi.IsDir() {
		return fmt.Errorf("--copy-store-from: %s is not a store's file", from)
	}
	loc, err := e.StoreLocation()
	if err != nil {
		return err
	}
	if err := e.MakeDataDir(); err != nil { // the destination's key file's folder
		return err
	}
	src, err := store.Open(ctx, from)
	if errors.Is(err, store.ErrBusy) {
		return fmt.Errorf("a daemon is serving %s: stop it first (--call sys.shutdown), then --copy-store-from", from)
	}
	if err != nil {
		return err
	}
	defer src.Close()
	if applied := src.Schema().Pending; len(applied) > 0 {
		fmt.Fprintf(out, "applied this build's scripts to %s, as its daemon's next start would: %s\n", from, strings.Join(applied, ", "))
	}
	dst, err := store.OpenAt(ctx, loc)
	if errors.Is(err, store.ErrBusy) {
		return fmt.Errorf("%w: a daemon serves the PostgreSQL store in schema %s: stop it first, then --copy-store-from", err, loc.Schema)
	}
	if err != nil {
		return err
	}
	defer dst.Close()
	rep, err := store.CopySQLite(ctx, dst, src)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "copied %s into the PostgreSQL store in schema %s:\n", from, loc.Schema)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, t := range rep.Tables {
		next := ""
		if t.Identity != "" {
			next = fmt.Sprintf("next %s %d", t.Identity, t.Next)
		}
		fmt.Fprintf(tw, "  %s\t%d rows\t%s\n", t.Name, t.Rows, next)
	}
	_ = tw.Flush()
	switch rep.Key {
	case "copied":
		fmt.Fprintf(out, "the store's key: the source's, written to %s\n", loc.KeyFile)
	case "matched":
		fmt.Fprintf(out, "the store's key: %s already held the source's\n", loc.KeyFile)
	default:
		fmt.Fprintln(out, "the store's key: the source has none (it keeps no sealed secret)")
	}
	fmt.Fprintf(out, "not done by the copy: %s is left as it was, apart from the scripts said above; no daemon was started; "+
		"the server's approximate-search indexes are built by their own job, not by the copy\n", from)
	return nil
}
