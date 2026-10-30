package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search/rank"
	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/edition"
	"github.com/yongjohnlee80/autodoc/core/fsbounds"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/sqlitejournal"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
	"github.com/yongjohnlee80/autodoc/internal/peercred"
	"github.com/yongjohnlee80/autodoc/internal/profiling"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// errAlreadyServing is this store's daemon already answering, on this endpoint or the one its
// lease-info names: this process serves nothing, and exits 0.
var errAlreadyServing = errors.New("autodoc is already serving")

// runServe is --serve (ADR 0203 §3): bind the endpoint, open the store (its lease makes this the
// only daemon serving it), open every workspace in it and start its indexer and follower (the
// follower watches, then reconciles), and serve the API until ctx ends or a client says
// sys.shutdown. The subsystems start in serveSteps' order, and each pushes how to stop what it
// started: they stop in reverse, the lease-info first and the socket last, whether serving ended or
// a later step failed.
func runServe(ctx context.Context, configPath string, out io.Writer, b build, prof *profiling.Profiler) error {
	e, err := endpoint.Load(configPath)
	if err != nil {
		return err
	}
	p, err := e.Paths()
	if err != nil {
		return err
	}
	d := &process{ctx: ctx, out: out, b: b, e: e, p: p, prof: prof, log: logger.New(logger.WithWriter(out))}
	defer d.stops.unwind()
	for _, step := range serveSteps {
		if err := step.start(d); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "autodoc %s serving msgpack-RPC on %s\n", b.version, p.Socket)
	return d.srv.Run(ctx)
}

// process is a --serve process as its steps build it.
type process struct {
	ctx   context.Context
	out   io.Writer
	b     build
	prof  *profiling.Profiler // the process's monitor (--profile, sys.profile); nil: none
	e     *endpoint.Endpoints
	p     endpoint.Paths
	log   logger.Logger
	stops stack

	ln      net.Listener
	db      *store.Store
	storeID string
	wsCtx   context.Context
	ranking *serving.Ranking
	ws      *serving.Workspaces
	emb     *serving.Embedding
	files   *docs.Docs
	guard   *fsbounds.Guard // a gated script's bounds (ADR 1792004705 §4.2a.1), over every workspace's files
	srv     *rpc.Server
	deps    *moduleDeps     // the modules' dependencies, built with the workspaces
	mods    []module.Module // the modules, built before any workspace starts
}

// serveSteps are --serve's subsystems in the order they start.
var serveSteps = []struct {
	name  string
	start func(*process) error
}{
	{"socket", (*process).bind},
	{"store", (*process).openStore},
	{"workspaces", (*process).startWorkspaces},
	{"files", (*process).openFiles},
	{"rpc", (*process).buildServer},
}

// stack is how to stop what a daemon started, run in reverse by unwind.
type stack []func()

func (s *stack) push(stop func()) { *s = append(*s, stop) }

func (s *stack) unwind() {
	for i := len(*s) - 1; i >= 0; i-- {
		(*s)[i]()
	}
	*s = nil
}

// bind makes the state and data directories (the socket's, with no runtime directory, and the
// store's) and binds the endpoint. An endpoint held by this store's daemon is errAlreadyServing.
func (d *process) bind() error {
	sock, storePath := d.p.Socket, d.p.Store
	if err := d.e.MakeStateDir(); err != nil {
		return err
	}
	if err := d.e.MakeDataDir(); err != nil {
		return err
	}
	if err := peercred.CheckDir(filepath.Dir(sock)); err != nil {
		return fmt.Errorf("bind %s: %w", sock, err)
	}
	ln, err := listen(sock)
	if errors.Is(err, syscall.EADDRINUSE) {
		probeCtx, cancel := context.WithTimeout(d.ctx, 3*time.Second)
		info, perr := rpc.Probe(probeCtx, "unix", sock)
		cancel()
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
	d.ln = peercred.Listener(ln, func(err error) { logger.Warning(d.log, err, "refused a connection") })
	d.stops.push(func() { removeIfStillOurs(sock, id) })
	return nil
}

// openStore opens the store, taking its lease, and applies its schema scripts.
func (d *process) openStore() error {
	storePath := d.p.Store
	noteOldIndexes(d.p.StateDir, storePath, d.out)
	db, err := store.Open(d.ctx, storePath)
	if errors.Is(err, store.ErrBusy) {
		// the store's daemon serves on another endpoint: a config naming another socket for the
		// same store finds it there, through the lease-info, and leaves it be
		if addr, ok := endpoint.Verified(d.ctx, storePath); ok {
			return fmt.Errorf("%w on %s, for %s; this process is not serving", errAlreadyServing, addr, storePath)
		}
		// the lease is held and no lease-info says by whom: the daemon before this one is closing
		// (it removes its lease-info first and lets go of the lease last). Wait for it rather than
		// exit and leave the client that started this process with no daemon at all.
		if _, lerr := store.ReadLeaseInfo(storePath); errors.Is(lerr, fs.ErrNotExist) {
			db, err = openAfterPredecessor(d.ctx, storePath)
		}
	}
	if err != nil {
		return err
	}
	d.db = db
	d.stops.push(func() { _ = db.Close() })
	if d.storeID, err = store.Identity(storePath); err != nil {
		return err
	}
	if st := db.Schema(); len(st.Pending) > 0 {
		fmt.Fprintf(d.out, "store %s: schema scripts applied: %s\n", storePath, strings.Join(st.Pending, ", "))
	}
	for _, w := range db.Schema().Warnings {
		logger.Warning(d.log, nil, w)
	}
	return nil
}

// newGuard is fsbounds.NewGuard, a var so a cell can make it fail as a folder that cannot be
// resolved would.
var newGuard = fsbounds.NewGuard

// startWorkspaces starts the ranker and the embedding provider, then every workspace in the store.
// They stop in reverse: the workspaces, their context, then the provider's and the ranker's last
// calls are written before the store closes.
func (d *process) startWorkspaces() error {
	cfg, b, db := d.e.Config, d.b, d.db
	// a gated script never reaches the daemon's own: the store's folder, STATE, and its config and the
	// folder beside it (lua/, plugins/ and the rest the TUI loads), whichever workspace holds them
	var err error
	if d.guard, err = newGuard(filepath.Dir(d.p.Store), d.p.StateDir, d.e.ConfigPath, filepath.Dir(d.e.ConfigPath)); err != nil {
		return err
	}
	wsCtx, stopWorkspaces := context.WithCancel(d.ctx)
	d.wsCtx = wsCtx
	// one ranker for the daemon (ADR 0215), set up before any workspace starts, so each first
	// search is ranked
	holder := &rank.Holder{}
	d.ranking = serving.NewRanking(db, holder, serving.Supplied{Ranker: b.rank.Ranker, Window: b.rank.Window}, d.log)
	d.ranking.Start(wsCtx)
	d.stops.push(d.ranking.Wait)
	d.ws = serving.New(wsCtx, db, serving.Options{Poll: cfg.Follow.PollInterval.Duration, Log: d.log,
		MaxEmbedRequests: cfg.EmbeddingQueue.MaxInflight, Databases: edition.Databases, Registrations: b.reg,
		Rank: index.Rank{Source: holder, Texts: b.rank.Texts, Required: b.rank.Required}, Guard: d.guard})
	// the modules, the todo system first among them, before any workspace starts: a workspace's
	// index reads the todo stores, so its first pass indexes their tasks graph-only (ADR 1792015212)
	d.deps = &moduleDeps{store: db, storeID: d.storeID, workspaces: d.ws, journal: sqlitejournal.New(db), log: d.log,
		onStores: d.ws.TodoStoresChanged}
	d.mods = modules(d.deps)
	d.ws.SetTodoStores(todoStores(d.deps.todos))
	// the provider the preferences name, before any workspace starts, so each starts with it
	d.emb = serving.NewEmbedding(db, d.ws, d.log)
	d.emb.Start(wsCtx)
	d.stops.push(d.emb.Wait)
	d.stops.push(stopWorkspaces)
	d.stops.push(d.ws.StopAll)
	// the reserved profiles the crew's mail needs (the daemon's, which sends every notice, and the
	// human's), made once; then the wake scheduler, which phase 1d starts here
	if err := d.deps.mail.EnsureReserved(d.ctx); err != nil {
		return err
	}
	d.startWakes()
	if err := d.ws.OpenAll(); err != nil {
		return err
	}
	// every workspace keeps only its active and target models' vectors; the store is compacted
	// once (ADR 1791284787): in the background, while the daemon serves
	go d.ws.SweepModels(wsCtx)
	if len(d.ws.List()) == 0 {
		logger.Warning(d.log, nil, "no workspace yet: add one in the TUI's workspace manager (autodoc --ui, then w) or with workspace.add")
	}
	return nil
}

// startWakes starts the wake scheduler (ADR 1793271500 §4.7) for this daemon run, its stop pushed
// onto the daemon's.
func (d *process) startWakes() {
	d.stops.push(startScheduler(d.ctx, d.deps.crew, crewWakers, d.log))
}

// crewWakers are the wakers by endpoint kind. Phase 3 brings them, one CLI at a time; until then
// there are none.
var crewWakers = map[string]crew.Waker{}

// startScheduler makes the wake scheduler for a fresh run id, and answers how to stop it. Making it
// is enough for the long-polls: every committed wake change pokes it, and its Watch serves
// mail.wait, crew.wait and inbox.watch. Its loop, which reserves and makes wake attempts, runs only
// once a waker exists: with none, every session with an endpoint would fail each attempt
// (no_waker) and, after five, be journaled unreachable, filling the human's feed with false
// reports.
func startScheduler(ctx context.Context, c *crew.Manager, wakers map[string]crew.Waker, log logger.Logger, opts ...crew.SchedulerOption) (stop func()) {
	opts = append([]crew.SchedulerOption{crew.WithWakeErrors(func(err error) { logger.Warning(log, err, "the wake scheduler") })}, opts...)
	s := c.NewScheduler(newRunID(), wakers, opts...)
	if len(wakers) == 0 {
		logger.Info(log, "the wake scheduler waits for its wakers (phase 3): mail is received when agents ask")
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Warning(log, err, "the wake scheduler stopped")
		}
	}()
	return func() { cancel(); <-done }
}

// newRunID is a fresh random id: this daemon run's, which the scheduler's attempts carry, so the
// next run recovers what this one left in flight.
func newRunID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b[:])
}

// openFiles opens a local peer's files outside every workspace (ADR 1791430651): the whole disk,
// every file.
func (d *process) openFiles() error {
	disk, err := local.New("/", local.WithLogger(d.log))
	if err != nil {
		return err
	}
	d.stops.push(func() { _ = disk.Close() })
	// a bounded file.* is routed into its workspace (rpc's boundedFile) and never reaches "/": behind no
	// guard at all, every bounded operation here is refused, should a route ever skip that
	var none *fsbounds.Guard
	d.files = docs.New(none.Wrap(func() string { return "" }, "/", disk), nil, docs.WithRegistrations(d.b.reg.Kinds()), docs.WithDeriver(d.b.reg))
	return nil
}

// buildServer builds the API over every subsystem and the modules, then says where it serves in
// the lease-info beside the store: the lease and the socket are both this process's now.
func (d *process) buildServer() error {
	b, db := d.b, d.db
	d.srv = rpc.New(d.ws, b.version, rpc.WithListener(d.ln), rpc.WithLogger(d.log), rpc.WithPreferences(db), rpc.WithEmbeddings(d.emb), rpc.WithEvents(db),
		rpc.WithRegistrations(b.reg.Tables()), rpc.WithRankers(d.ranking), rpc.WithStoreID(d.storeID), rpc.WithFiles(d.files), rpc.WithGuard(d.guard),
		rpc.WithModules(d.mods...), rpc.WithProfiler(d.prof))
	d.refreshTodos(d.deps.todos)
	li := store.LeaseInfo{StoreID: d.storeID, StorePath: d.p.Store, Addr: d.p.Socket, PID: int64(os.Getpid()), Instance: d.srv.Instance(),
		Version: b.version, Protocol: rpc.Protocol, MinProtocol: rpc.MinProtocol, Since: time.Now().UTC()}
	if err := store.WriteLeaseInfo(d.p.Store, li); err != nil {
		logger.Warning(d.log, err, "the lease-info was not written: a client with another socket for this store will not find this daemon")
	}
	instance := d.srv.Instance()
	d.stops.push(func() { _ = store.RemoveLeaseInfo(d.p.Store, instance) })
	return nil
}

// refreshTodos runs the todo system's daily refresh of claimed stores (ADR 1791532020 §2.3) for as
// long as the daemon serves. It stops, its refresh in progress ended, before the store closes.
func (d *process) refreshTodos(m *todo.Manager) {
	ctx, cancel := context.WithCancel(d.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.RefreshDaily(ctx)
	}()
	d.stops.push(func() { cancel(); <-done })
}

// moduleDeps are what the modules' handlers use. nil builds the modules for their contracts alone
// (--contracts), which never touch them.
type moduleDeps struct {
	store      *store.Store
	storeID    string // the store's identity, which a session context names (crew.whoami)
	workspaces rpc.Workspaces
	journal    todo.Journal // the todos' change log, in the store
	log        logger.Logger

	todos *todo.Manager // the todo system modules built, for its daily refresh
	// the crew, its mail and the human's inbox (ADR 1793271500), on one store
	crew  *crew.Manager
	mail  *mail.Manager
	inbox *inbox.Manager
	// onStores is told a todo store was registered, moved or removed, or its variables changed: the
	// workspaces read the stores again (ADR 1792015212 §2.2). nil: no one is.
	onStores func()
}

// modules are the daemon's core Modules, the one list of them: runServe serves them, and
// --contracts and AGENTS.md's tables list them.
func modules(d *moduleDeps) []module.Module {
	if d == nil {
		return []module.Module{todo.New(nil, nil, nil), crew.New(nil), mail.New(nil, nil), inbox.New(nil, nil, nil)}
	}
	opts := []todo.Option{todo.WithLogger(d.log), todo.WithEvents(func(e todo.Event) { todoEvent(d, e) })}
	if d.onStores != nil {
		opts = append(opts, todo.WithStoresChanged(d.onStores)) // the workspaces read the stores again
	}
	if home, err := os.UserHomeDir(); err == nil {
		opts = append(opts, todo.WithHome(home)) // $HOME and a ref's leading ~ (ADR 1792015069 §2.2)
	}
	d.todos = todo.New(serving.TodoRoots{Workspaces: d.workspaces}, serving.TodoDisk{Log: d.log}, d.journal, opts...)
	crewModules(d)
	return []module.Module{d.todos, d.crew, d.mail, d.inbox}
}

// crewModules builds the crew, mail and inbox modules on one store (ADR 1793271500 §4.1): every
// transaction is crew's, mail's and the inbox's at once, so a verb's guard, crew's change and
// mail's effect commit together. Mail authenticates through crew, and crew ends a session's mail
// through mail (its SessionEnder), so mail's port to crew is set once crew exists. A committed
// write tells the human feed's waiters (inbox.Feed) and, when it changed a wake row, the wake
// scheduler and every long-poll (crew's Poke). One limiter caps each connection's long-polls
// across the three modules.
func crewModules(d *moduleDeps) {
	feed := inbox.NewFeed()
	polls := crew.NewLongPolls()
	st := sqlitecrewmail.New(d.store, sqlitecrewmail.WithSignals(crew.Signals{Feed: feed.Notify,
		Wake: func() { d.crew.Poke() }}))
	port := &crewPort{}
	d.mail = mail.New(st.Mail(), port, mail.WithLongPolls(polls))
	d.crew = crew.New(st, crew.WithSessionEnder(d.mail), crew.WithStoreID(d.storeID), crew.WithLongPolls(polls))
	port.Manager = d.crew
	d.inbox = inbox.New(st.Inbox(), d.mail, feed, inbox.WithLongPolls(polls))
}

// crewPort is mail's port to crew (mail.Crew), set once crew is built: the two depend on each
// other. It embeds the crew Manager, so the port follows whatever mail.Crew asks of crew.
type crewPort struct{ *crew.Manager }

// todoEvent records in sys.events what the todo system saw beyond its verbs' own events: an
// ambiguous change, a blocked store move, a consumer dropped, a root registered, a daily refresh
// that changed a store or failed.
func todoEvent(d *moduleDeps, e todo.Event) {
	detail := strings.Join(slices.DeleteFunc([]string{e.Path, e.ID, e.Detail}, func(s string) bool { return s == "" }), " · ")
	if _, err := d.store.AppendEvent(context.Background(), store.Event{Kind: e.Kind, Workspace: e.Workspace, Detail: detail}); err != nil {
		logger.Warning(d.log, err, "logging event "+e.Kind)
	}
}

// Contracts is the catalog of every verb this build serves, rpc's own and its modules', that filter
// names ("" or "*" for all, "domain.*", or a verb), at this build's protocol: built in this process,
// with no daemon. internal/contractsgen writes AGENTS.md's tables from it.
func Contracts(filter string) rpc.Catalog {
	return rpc.New(rpc.Fixed(), "", rpc.WithModules(modules(nil)...)).Catalog(rpc.Protocol, filter)
}

// writeContracts prints c as JSON, or as AGENTS.md's tables (md).
func writeContracts(w io.Writer, c rpc.Catalog, format string) error {
	switch format {
	case "json":
		b, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "%s\n", b)
		return err
	case "md":
		_, err := io.WriteString(w, c.Markdown())
		return err
	}
	return fmt.Errorf("--format %q: json or md", format)
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
			// NOTE: polled, not waited on. The lease is another process's flock, and nothing tells this
			// one when it is released; a blocking flock could not be bounded by predecessorWait or ctx. A
			// try every 100 ms keeps a restart's handoff quick.
		case <-time.After(100 * time.Millisecond):
		}
		db, err := store.Open(ctx, storePath)
		if !errors.Is(err, store.ErrBusy) {
			return db, err
		}
		if addr, ok := endpoint.Verified(ctx, storePath); ok {
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

// todoStores is the todo registry as the workspaces read it (ADR 1792015212 §2.2, ADR 1792132105
// §2.5): each store's id, directory and workspace, and every workspace's variables.
func todoStores(m *todo.Manager) serving.TodoStores {
	return func(ctx context.Context) (serving.TodoRegistry, error) {
		list, err := m.Stores(ctx)
		if err != nil {
			return serving.TodoRegistry{}, err
		}
		reg := serving.TodoRegistry{Stores: make([]serving.TodoStore, 0, len(list)), Check: todo.CheckFile}
		for _, s := range list {
			reg.Stores = append(reg.Stores, serving.TodoStore{ID: s.ID, Dir: s.Path, KB: s.KB})
		}
		if reg.Vars, err = m.AllEffectiveVars(ctx); err != nil {
			return serving.TodoRegistry{}, fmt.Errorf("the workspaces' variables: %w", err)
		}
		for _, root := range slices.Sorted(maps.Keys(reg.Vars)) {
			if root != "" {
				reg.Files = append(reg.Files, filepath.Join(root, todo.VarsFile))
			}
		}
		return reg, nil
	}
}
