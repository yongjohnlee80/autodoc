package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/edition"
	"github.com/yongjohnlee80/autodoc/core/store"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// errAlreadyServing is a compatible AutoDoc answering on the endpoint: this process serves nothing.
var errAlreadyServing = errors.New("autodoc is already serving")

// runServe is --serve (ADR 0203 §3): bind the endpoint, open the store (its lease makes this the
// only daemon serving it), open every workspace in it and start its indexer and follower (the
// follower watches, then reconciles), and serve the API until ctx ends or a client says
// sys.shutdown. Then it drains, stops every workspace, closes the store, and removes the socket if
// it is still this process's.
func runServe(ctx context.Context, configPath string, out io.Writer) error {
	log := logger.New(logger.WithWriter(out))
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
	sock, err := cfg.Server.SocketPath()
	if err != nil {
		return err
	}
	ln, err := listen(sock)
	if errors.Is(err, syscall.EADDRINUSE) {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if v, perr := rpc.ProbeOn(probeCtx, "unix", sock); perr == nil {
			return fmt.Errorf("%w on %s (version %s); this process is not serving", errAlreadyServing, sock, v)
		} else {
			return fmt.Errorf("bind %s: in use by something that is not a compatible autodoc: %v", sock, perr)
		}
	}
	if err != nil {
		return fmt.Errorf("bind %s: %w", sock, err)
	}
	id, err := ownSocket(ln, sock)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer removeIfStillOurs(sock, id)

	storePath, err := cfg.Server.StorePath()
	if err != nil {
		return err
	}
	if stateDir, err := cfg.Server.StateDirPath(); err == nil {
		noteOldIndexes(stateDir, storePath, out)
	}
	db, err := store.Open(ctx, storePath)
	if err != nil {
		return err
	}
	defer db.Close()
	if st := db.Schema(); len(st.Pending) > 0 {
		fmt.Fprintf(out, "store %s: schema scripts applied: %s\n", storePath, strings.Join(st.Pending, ", "))
	}
	for _, w := range db.Schema().Warnings {
		logger.Warning(log, nil, w)
	}
	wsCtx, stopWorkspaces := context.WithCancel(ctx)
	ws := serving.New(wsCtx, db, serving.Options{Poll: cfg.Follow.PollInterval.Duration, Log: log,
		MaxEmbedRequests: cfg.EmbeddingQueue.MaxInflight, Databases: edition.Databases})
	// the provider the preferences name, before any workspace starts, so each starts with it; the
	// last calls its meter heard are written before the store closes
	emb := serving.NewEmbedding(db, ws, log)
	emb.Start(wsCtx)
	defer emb.Wait()
	defer stopWorkspaces()
	defer ws.StopAll()
	if err := ws.OpenAll(); err != nil {
		return err
	}
	if len(ws.List()) == 0 {
		logger.Warning(log, nil, "no workspace yet: add one in the TUI's workspace manager (autodoc --ui, then w) or with workspace.add")
	}
	srv := rpc.New(ws, version, rpc.WithListener(ln), rpc.WithLogger(log), rpc.WithPreferences(db), rpc.WithEmbeddings(emb), rpc.WithEvents(db))
	fmt.Fprintf(out, "autodoc %s serving msgpack-RPC on %s\n", version, sock)
	return srv.Run(ctx)
}

// noteOldIndexes says once where the index files of the builds before the store are: they are no
// longer read, and nothing deletes them, since a person may want them. A marker beside them keeps
// it to once.
func noteOldIndexes(stateDir, storePath string, out io.Writer) {
	dir := filepath.Join(stateDir, "workspaces")
	old, _ := filepath.Glob(filepath.Join(dir, "*", "index.db"))
	marker := filepath.Join(dir, ".noted")
	if len(old) == 0 {
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return
	}
	fmt.Fprintf(out, "the per-workspace index files of earlier builds are no longer used (the index is in %s now); delete them when you like:\n", storePath)
	for _, f := range old {
		fmt.Fprintln(out, "  "+f)
	}
	_ = os.WriteFile(marker, []byte("the files beside this were noted as unused\n"), 0o600)
}
