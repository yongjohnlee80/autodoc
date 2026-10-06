package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/edition"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// errAlreadyServing is this store's daemon already answering, on this endpoint or the one its
// lease-info names: this process serves nothing, and exits 0.
var errAlreadyServing = errors.New("autodoc is already serving")

// runServe is --serve (ADR 0203 §3): bind the endpoint, open the store (its lease makes this the
// only daemon serving it), open every workspace in it and start its indexer and follower (the
// follower watches, then reconciles), and serve the API until ctx ends or a client says
// sys.shutdown. Then it drains, stops every workspace, closes the store, and removes the socket if
// it is still this process's.
func runServe(ctx context.Context, configPath string, out io.Writer, b build) error {
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
	storePath, err := cfg.Server.StorePath()
	if err != nil {
		return err
	}
	ln, err := listen(sock)
	if errors.Is(err, syscall.EADDRINUSE) {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		info, perr := rpc.Probe(probeCtx, "unix", sock)
		switch {
		case perr != nil:
			return fmt.Errorf("bind %s: in use by something that is not an autodoc: %v", sock, perr)
		case servesStore(info, storePath):
			return fmt.Errorf("%w on %s (version %s); this process is not serving", errAlreadyServing, sock, info.Version)
		default:
			return fmt.Errorf("bind %s: in use by an autodoc %s (protocol %d) that is not serving %s", sock, info.Version, info.Protocol, storePath)
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

	if stateDir, err := cfg.Server.StateDirPath(); err == nil {
		noteOldIndexes(stateDir, storePath, out)
	}
	db, err := store.Open(ctx, storePath)
	if errors.Is(err, store.ErrBusy) {
		// the store's daemon serves on another endpoint: a config naming another socket for the
		// same store finds it there, through the lease-info, and leaves it be
		if addr, ok := verifiedHolder(ctx, storePath); ok {
			return fmt.Errorf("%w on %s, for %s; this process is not serving", errAlreadyServing, addr, storePath)
		}
		// the lease is held and no lease-info says by whom: the daemon before this one is closing
		// (it removes its lease-info first and lets go of the lease last). Wait for it rather than
		// exit and leave the client that started this process with no daemon at all.
		if _, lerr := store.ReadLeaseInfo(storePath); errors.Is(lerr, fs.ErrNotExist) {
			db, err = openAfterPredecessor(ctx, storePath)
		}
	}
	if err != nil {
		return err
	}
	defer db.Close()
	storeID, err := store.Identity(storePath)
	if err != nil {
		return err
	}
	if st := db.Schema(); len(st.Pending) > 0 {
		fmt.Fprintf(out, "store %s: schema scripts applied: %s\n", storePath, strings.Join(st.Pending, ", "))
	}
	for _, w := range db.Schema().Warnings {
		logger.Warning(log, nil, w)
	}
	wsCtx, stopWorkspaces := context.WithCancel(ctx)
	// one ranker for the daemon (ADR 0215), set up before any workspace starts, so each first
	// search is ranked; its last calls are written before the store closes
	holder := &rank.Holder{}
	ranking := serving.NewRanking(db, holder, serving.Supplied{Ranker: b.rank.Ranker, Window: b.rank.Window}, log)
	ranking.Start(wsCtx)
	defer ranking.Wait()
	ws := serving.New(wsCtx, db, serving.Options{Poll: cfg.Follow.PollInterval.Duration, Log: log,
		MaxEmbedRequests: cfg.EmbeddingQueue.MaxInflight, Databases: edition.Databases, Registrations: b.reg,
		Rank: index.Rank{Source: holder, Texts: b.rank.Texts, Required: b.rank.Required}})
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
	// every workspace keeps only its active and target models' vectors; the store is compacted
	// once (ADR 1791284787): in the background, while the daemon serves
	go ws.SweepModels(wsCtx)
	if len(ws.List()) == 0 {
		logger.Warning(log, nil, "no workspace yet: add one in the TUI's workspace manager (autodoc --ui, then w) or with workspace.add")
	}
	srv := rpc.New(ws, b.version, rpc.WithListener(ln), rpc.WithLogger(log), rpc.WithPreferences(db), rpc.WithEmbeddings(emb), rpc.WithEvents(db),
		rpc.WithRegistrations(b.reg.Tables()), rpc.WithRankers(ranking), rpc.WithStoreID(storeID))
	// the lease and the socket are both this process's now: say where it serves, beside the store
	li := store.LeaseInfo{StoreID: storeID, StorePath: storePath, Addr: sock, PID: int64(os.Getpid()), Instance: srv.Instance(),
		Version: b.version, Protocol: rpc.Protocol, MinProtocol: rpc.MinProtocol, Since: time.Now().UTC()}
	if err := store.WriteLeaseInfo(storePath, li); err != nil {
		logger.Warning(log, err, "the lease-info was not written: a client with another socket for this store will not find this daemon")
	}
	defer func() { _ = store.RemoveLeaseInfo(storePath, srv.Instance()) }()
	fmt.Fprintf(out, "autodoc %s serving msgpack-RPC on %s\n", b.version, sock)
	return srv.Run(ctx)
}

// predecessorWait bounds how long a daemon waits for the one before it to let go of the store: its
// shutdown drains, stops every workspace and closes the store, well within a client's probe window
// (tui's 15 s).
var predecessorWait = 10 * time.Second

// openAfterPredecessor opens the store once the daemon closing it lets go of its lease, polling
// within predecessorWait. A daemon that comes up for the store meanwhile (its lease-info verified)
// is the one serving, and this process is not; a lease still held at the end is ErrBusy, as before.
func openAfterPredecessor(ctx context.Context, storePath string) (*store.Store, error) {
	deadline := time.Now().Add(predecessorWait)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		db, err := store.Open(ctx, storePath)
		if !errors.Is(err, store.ErrBusy) {
			return db, err
		}
		if addr, ok := verifiedHolder(ctx, storePath); ok {
			return nil, fmt.Errorf("%w on %s, for %s; this process is not serving", errAlreadyServing, addr, storePath)
		}
		if time.Now().After(deadline) {
			return nil, err
		}
	}
}

// servesStore reports whether a probed daemon serves the store at storePath: its store_id is that
// store's identity. A daemon too old to say is not taken for it.
func servesStore(info rpc.ProbeInfo, storePath string) bool {
	id, err := store.Identity(storePath)
	return err == nil && info.StoreID != "" && info.StoreID == id
}

// verifiedHolder is the endpoint of the daemon holding the store's lease, when its lease-info names
// one that answers as this store's daemon and the same process the record was written by.
func verifiedHolder(ctx context.Context, storePath string) (string, bool) {
	li, err := store.ReadLeaseInfo(storePath)
	if err != nil || li.Addr == "" {
		return "", false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	info, err := rpc.Probe(probeCtx, "unix", li.Addr)
	if err != nil || !servesStore(info, storePath) || info.Instance != li.Instance {
		return "", false
	}
	return li.Addr, true
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
