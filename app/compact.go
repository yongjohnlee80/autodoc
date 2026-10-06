package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// runCompact is --compact (ADR 1791284787 §2.5): with the daemon stopped, reclaim every workspace's
// models that are neither active nor the target, then rewrite the store with VACUUM, behind its
// free-space check. Incremental vacuum returns freed pages but leaves the rows kept where they were;
// a store that switches models often is made contiguous again this way. The daemon serving the store
// holds its lease, so a daemon running is refused, saying how to stop it.
func runCompact(ctx context.Context, configPath string, out io.Writer) error {
	if configPath == "" {
		var err error
		if configPath, err = config.DefaultPath(); err != nil {
			return err
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	storePath, err := cfg.Server.StorePath()
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, storePath)
	if errors.Is(err, store.ErrBusy) {
		return fmt.Errorf("the daemon is serving %s: stop it first (--call sys.shutdown), then --compact", storePath)
	}
	if err != nil {
		return err
	}
	defer db.Close()
	ws, err := db.Workspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		got, err := index.SweepStore(ctx, db, w.ID)
		if err != nil {
			return fmt.Errorf("reclaiming %s's models: %w", w.Name, err)
		}
		for _, r := range got {
			fmt.Fprintf(out, "%s: reclaimed %s, %d vectors\n", w.Name, r.FP, r.Vectors)
		}
	}
	c, err := db.Compact(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "compacted %s from %d MB to %d MB\n", storePath, c.Before>>20, c.After>>20)
	return nil
}
