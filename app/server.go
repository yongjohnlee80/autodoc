//go:build server

package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/pglease"
	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/api"
	"github.com/yongjohnlee80/autodoc/enterprise/config"
	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
	"github.com/yongjohnlee80/autodoc/enterprise/ingest"
	"github.com/yongjohnlee80/autodoc/enterprise/ops"
	"github.com/yongjohnlee80/autodoc/enterprise/ranker"
)

// runServer is --server: the enterprise search server, headless, with no socket, no TUI and no
// SQLite. It reads server.toml at path and every source's view, refusing (a *config.Refusal, exit
// 2) anything wrong, naming the field, before it opens anything. With check, it then confirms each
// view's order against the live source, says what it checked on stdout, and returns.
//
// Otherwise it logs to stderr, and its subsystems start in serverSteps' order, each pushing how to
// stop what it started: they stop in reverse when ctx ends (SIGTERM) or an ingester fails (a lost
// lease), the embedding worker first, then the ingesters, releasing their tenants' leases, and the
// pools last. level, when not "", is --log-level, over the file's [log] level.
func runServer(ctx context.Context, path string, check bool, level logger.Severity, stdout, stderr io.Writer, version string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	sources, err := loadSources(path, cfg)
	if err != nil {
		return err
	}
	if err := refuseSmallIndexPool(path, cfg.Postgres); err != nil {
		return err
	}
	if check {
		return checkServer(ctx, path, cfg, sources, stdout)
	}
	if level == "" {
		level = cfg.Log.Level
	}
	s := &server{ctx: ctx, cfg: cfg, sources: sources, log: logger.New(logger.WithWriter(stderr), logger.WithContext("autodoc-server"), logger.WithMinLevel(level)), failed: make(chan error, len(sources)+1), version: version}
	defer s.stops.unwind()
	for _, step := range serverSteps {
		if err := step.start(s); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	logger.Info(s.log, fmt.Sprintf("autodoc %s serving enterprise search: %d sources in schema %s", version, len(sources), cfg.Postgres.Schema))
	select {
	case <-ctx.Done():
		return nil
	case err := <-s.failed:
		return err
	}
}

// server is a --server process as its steps build it.
type server struct {
	ctx     context.Context
	cfg     *config.Server
	sources []*ingest.Source
	log     logger.Logger
	stops   stack

	// store is pgstore's pool, every connection in the configured schema: the searches', the
	// ingesters' and the worker's, and every write's fence on its tenant's lease; business reads
	// the views on the role's own search_path; leases is the writer leases' own pool (leaseConns),
	// in the same schema, so no load on the store's can starve a lease's renew.
	store, business, leases dao.DataConn
	// failed carries the first ingester, or the HTTP server, that stopped with an error
	failed chan error
	// ingesters are the sources' ingesters, in the configuration's order: the embedding worker
	// writes a tenant only while its ingester holds the tenant's lease, through the ingester's
	// fenced Store.
	ingesters []*ingest.Ingester
	// worker is the embedding worker once it has started, which an ingester's commit wakes; nil
	// before, when a commit has nothing to wake (the worker's first round reads what it wrote).
	worker atomic.Pointer[embedder.Worker]
	// ann is the approximate semantic search every tenant's store shares, and its builder
	ann *pgstore.ANN
	// ops tells an operator the server's health and status, and logs the metrics lines; api is the
	// HTTP API once it serves, whose counts the metrics lines take
	ops     *ops.Collector
	api     atomic.Pointer[api.Handler]
	version string
	// ranker is [ranker]'s, every search's; nil when the server has none
	ranker *ranker.Ranker
}

// serverSteps are --server's subsystems in the order they start. The schema must exist before
// Migrate runs: with search_path set to the schema then public, a schema not yet created would
// make PostgreSQL create pgstore's tables in public.
var serverSteps = []struct {
	name  string
	start func(*server) error
}{
	{"postgres", (*server).openPostgres},
	{"schema", (*server).migrate},
	{"ann", (*server).startANN},
	{"policy", (*server).setPolicies},
	{"ingesters", (*server).startIngesters},
	{"embedder", (*server).startEmbedder},
	{"ranker", (*server).startRanker},
	{"ops", (*server).startOps},
	{"http", (*server).startHTTP},
}

// leaseConns is the leases' pool's size: a lease's acquire and renew and the health check's ping
// are one short statement each, a renew every few seconds per tenant.
//
// NOTE: a pool of their own, never the store's. A renew waiting for one of the store's connections
// behind a flood of searches (each holding one up to its budget) went silent past the lease's
// length, and losing the lease stopped the server: one client's 512 searches in flight on a pool
// of four did it in about 30 s.
const leaseConns = 2

// leaseTTL is the ingesters' writer leases' length, and the writer session's idle-transaction
// bound is twice it (pglease.WriterSession): DefaultLeaseTTL, which a cell shortens.
var leaseTTL = ingest.DefaultLeaseTTL

// openPostgres opens the store's and the leases' pools on the index's DSN and the business pool on
// the source's. A DSN is a secret: an error opening it is redacted of it.
func (s *server) openPostgres() error {
	open := func(dsn string, opts ...postgres.Option) (dao.DataConn, error) {
		conn, err := postgres.OpenNamed(s.ctx, "autodoc-server", dsn, opts...)
		if err != nil {
			return nil, redact(err, dsn)
		}
		s.stops.push(func() { _ = conn.Close() })
		return conn, nil
	}
	var err error
	// a writer's transaction left idle twice a lease's length is ended, so a hung one's fence
	// never keeps a standby from its tenant. The ingesters' leases are leaseTTL long: a
	// configurable length changes both together. The index, its leases and its vectors go through
	// the index's DSN; the views are read through the source's, the same database unless
	// [postgres] index_dsn_env names one of the index's own
	if s.store, err = open(s.cfg.Postgres.IndexDSN(), pgstore.WithSchema(s.cfg.Postgres.Schema), pglease.WriterSession(leaseTTL)); err != nil {
		return err
	}
	if s.leases, err = open(s.cfg.Postgres.IndexDSN(), pgstore.WithSchema(s.cfg.Postgres.Schema), pglease.WriterSession(leaseTTL), postgres.MaxOpenConns(leaseConns)); err != nil {
		return err
	}
	s.business, err = open(s.cfg.Postgres.DSN())
	return err
}

// migrate creates the schema, then applies pgstore's migrations in it.
func (s *server) migrate() error {
	if err := pgstore.EnsureSchema(s.ctx, s.store, s.cfg.Postgres.Schema); err != nil {
		return redact(err, s.cfg.Postgres.IndexDSN())
	}
	return redact(pgstore.Migrate(s.ctx, s.store), s.cfg.Postgres.IndexDSN())
}

// annTick is how often the ANN builder looks for an index to build, recover or retire: 30 s, which
// a cell shortens.
var annTick = 30 * time.Second

// noTempWarning is the start's one line for an index role that can't create temporary tables.
const noTempWarning = "ann: this role can't create temporary tables in the index's database, so the approximate search's index can't be built and searches by meaning stay exact: GRANT TEMPORARY ON DATABASE to the role (PostgreSQL grants it to every role unless it was revoked from PUBLIC)"

// startANN makes the approximate search every tenant's store shares, and runs its builder until
// the server stops: the per-model HNSW indexes, one built at a time across every server on the
// database. A builder's error is logged and retried at the next tick; searches stay exact
// meanwhile. A role that can't create temporary tables, so can build no index, is warned of once.
func (s *server) startANN() error {
	var run [8]byte
	if _, err := rand.Read(run[:]); err != nil {
		return err
	}
	s.ann = pgstore.NewANN(s.store, s.cfg.Postgres.ANN, hex.EncodeToString(run[:]))
	// One line at the start when the index's role can't create temporary tables, which every index
	// build needs. The server runs all the same: a deployment in that state searches exactly today,
	// and must not stop at an upgrade. But the builder's own warnings come a minute apart, then
	// less often, then stop, and a tenant too small to want the index reads "exact" either way.
	if db, err := pgstore.ReadIndexDatabase(s.ctx, s.store, s.cfg.Postgres.Schema); err == nil && !db.CanTemp {
		logger.Warning(s.log, nil, noTempWarning)
	}
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	s.stops.push(func() {
		cancel()
		<-done
	})
	go func() {
		defer close(done)
		s.ann.Run(ctx, annTick, func(err error) { logger.Warning(s.log, err, "ann: the index builder") })
	}()
	return nil
}

// tenantStore is tenant's store on the server's pool, with its approximate search.
func (s *server) tenantStore(tenant string) (*pgstore.Store, error) {
	return pgstore.New(s.store, tenant, pgstore.WithANN(s.ann))
}

// setPolicies records each tenant's access policy from the configuration: the fields every search
// of the tenant must be scoped by, the view it is indexed from, and its declared facets. A tenant
// whose last refresh wrote another declaration is refreshed at once by its ingester.
func (s *server) setPolicies() error {
	for i, src := range s.cfg.Sources {
		st, err := s.tenantStore(src.Tenant)
		if err != nil {
			return err
		}
		if err := st.SetPolicy(s.ctx, pgstore.Policy{AccessFields: src.Access, View: s.sources[i].Name(), Facets: src.Facets}); err != nil {
			return fmt.Errorf("tenant %s: %w", src.Tenant, err)
		}
	}
	return nil
}

// startIngesters runs one ingester per source until the server stops. The first that fails stops
// the server; stopping cancels them all and waits for each to release its lease.
func (s *server) startIngesters() error {
	ctx, cancel := context.WithCancel(s.ctx)
	var wg sync.WaitGroup
	s.stops.push(func() {
		cancel()
		wg.Wait()
	})
	for i, src := range s.cfg.Sources {
		st, err := s.tenantStore(src.Tenant)
		if err != nil {
			return err
		}
		in := ingest.New(st, src.Tenant, s.sources[i], s.business, s.leases, ingest.Options{LeaseTTL: leaseTTL,
			Refresh: src.Refresh, RefreshRetry: src.RefreshRetry, Interval: src.Interval, Lag: lagOf(src.Lag), Log: s.log, Committed: s.wakeEmbedder, Locked: s.wakeEmbedder})
		s.ingesters = append(s.ingesters, in)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := in.Run(ctx); err != nil {
				s.failed <- fmt.Errorf("tenant %s: %w", src.Tenant, err)
			}
		}()
	}
	return nil
}

// startEmbedder runs the one embedding worker over every tenant until the server stops; stopping
// cancels it and waits for its calls in flight. It never fails the server: a provider that cannot
// be set up or fails is retried while ingestion goes on, and searches answer by words meanwhile.
func (s *server) startEmbedder() error {
	tenants := make([]embedder.Tenant, len(s.ingesters))
	for i, in := range s.ingesters {
		// the ingester's Store: every write of the worker's to the tenant is fenced on its lease
		tenants[i] = embedder.Tenant{Name: in.Tenant(), Store: in.Store(), Confirm: in.Confirm}
	}
	w := embedder.New(providerOf(s.cfg.Embedding), tenants, embedder.WithSlots(s.cfg.Embedding.Slots),
		embedder.WithBatch(s.cfg.Embedding.Batch), embedder.WithCallTimeout(s.cfg.Embedding.CallTimeout), embedder.WithLogger(s.log))
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	s.stops.push(func() {
		cancel()
		<-done
	})
	s.worker.Store(w)
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	return nil
}

// wakeEmbedder wakes the embedding worker after an ingester's commit, and when an ingester takes
// its tenant's lease: the worker leaves a tenant alone until it is this server's.
func (s *server) wakeEmbedder() {
	if w := s.worker.Load(); w != nil {
		w.Wake()
	}
}

// lagOf is a configured lag as ingest.Options takes it, where zero is the default: a lag of zero
// configured is none.
func lagOf(d time.Duration) time.Duration {
	if d == 0 {
		return -1
	}
	return d
}

// loadSources loads every source's view, refusing each that cannot be indexed, naming its source.
func loadSources(path string, cfg *config.Server) ([]*ingest.Source, error) {
	out := make([]*ingest.Source, len(cfg.Sources))
	var refused config.Refusal
	for i, s := range cfg.Sources {
		src, err := ingest.LoadDeclared(s.View, s.Access, s.Facets)
		if err != nil {
			refused.Errors = append(refused.Errors, &config.Error{Field: fmt.Sprintf("source[%d].view", i), Reason: err.Error()})
			continue
		}
		if s.Deletes != "" {
			d, err := ingest.LoadDeletes(s.Deletes)
			if err != nil {
				refused.Errors = append(refused.Errors, &config.Error{Field: fmt.Sprintf("source[%d].deletes", i), Reason: err.Error()})
				continue
			}
			src = src.WithDeletes(d)
		}
		out[i] = src
	}
	if len(refused.Errors) > 0 {
		refused.Path = path
		return nil, &refused
	}
	return out, nil
}

// minIndexConns is the fewest connections the index's pool may have: an index build holds two at
// once, the session it pins for the DDL and a transaction beside it that renews the build's lease
// while the DDL runs. With one, the first build waits for its second connection for good, and every
// search, write and embedding waits behind it.
const minIndexConns = 2

// refuseSmallIndexPool refuses an index DSN whose pool_max_conns is under minIndexConns, naming the
// field whose variable holds it. The start and --check both ask it, before anything is opened.
func refuseSmallIndexPool(path string, p config.Postgres) error {
	n := poolSize(p.IndexDSN())
	if n >= minIndexConns {
		return nil
	}
	field := "postgres.dsn_env"
	if p.SeparateIndex() {
		field = "postgres.index_dsn_env"
	}
	return config.Refuse(path, field, fmt.Sprintf("its DSN sets pool_max_conns=%d: the index's pool needs at least %d, since an index build holds two connections at once; set pool_max_conns=%d or more, or leave it out",
		n, minIndexConns, minIndexConns))
}

// checkServer is --server --check, after the configuration and the views loaded: each view's order
// is confirmed against the live source, read-only, up to now − its lag, and the embedding provider
// is asked to embed a probe. It writes what it checked, and the configuration's warnings, to out.
func checkServer(ctx context.Context, path string, cfg *config.Server, sources []*ingest.Source, out io.Writer) error {
	for _, w := range cfg.Warnings {
		fmt.Fprintln(out, "warning:", w)
	}
	if cfg.Postgres.SeparateIndex() {
		if err := checkIndex(ctx, path, cfg.Postgres, out); err != nil {
			return err
		}
	}
	dsn := cfg.Postgres.DSN()
	// which database didn't answer is said, as the index's is: with two DSNs, the driver's words
	// (a user, an address) leave the reader to work out which variable to mend
	source := "the source's database"
	if !cfg.Postgres.SeparateIndex() {
		source = "the database"
	}
	business, err := postgres.OpenNamed(ctx, "autodoc-server", dsn)
	if err != nil {
		return fmt.Errorf("%s (postgres.dsn_env): %w", source, redact(err, dsn))
	}
	defer business.Close()
	now, err := pgstore.Now(ctx, business)
	if err != nil {
		return fmt.Errorf("%s (postgres.dsn_env): %w", source, redact(err, dsn))
	}
	for i, src := range cfg.Sources {
		rep, err := sources[i].CheckOrder(ctx, business, now.Add(-src.Lag))
		var ve *ingest.ViewError
		if errors.As(err, &ve) {
			return config.Refuse(path, fmt.Sprintf("source[%d].view", i), ve.Error())
		}
		if err != nil {
			return fmt.Errorf("source[%d] (%s): %w", i, src.Tenant, redact(err, dsn))
		}
		more := ""
		if rep.Capped {
			more = fmt.Sprintf(" (the first %d)", ingest.CheckRows)
		}
		fmt.Fprintf(out, "source[%d] %s: %s: order confirmed over %d rows%s in %d pages: %d tied rows, %d page boundaries resumed\n",
			i, src.Tenant, sources[i].File(), rep.Rows, more, rep.Pages, rep.Ties, rep.Boundaries)
		for _, w := range rep.Warnings {
			fmt.Fprintf(out, "warning: source[%d] %s: %s\n", i, src.Tenant, w)
		}
	}
	if err := checkEmbedding(ctx, path, cfg.Embedding, out); err != nil {
		return err
	}
	if cfg.Ranker != nil {
		if err := checkRanker(ctx, path, *cfg.Ranker, out); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "%s: ok\n", path)
	return nil
}

// checkIndex is --check of the index's own database ([postgres] index_dsn_env): it answers, the
// vector extension is installed or the role can install it, the role can create the index's
// tables in its schema, or the schema, and it can create temporary tables, which an index build
// uses. It reads only, and writes one line saying what it found.
func checkIndex(ctx context.Context, path string, p config.Postgres, out io.Writer) error {
	dsn := p.IndexDSN()
	conn, err := postgres.OpenNamed(ctx, "autodoc-server", dsn)
	if err != nil {
		return fmt.Errorf("the index's database: %w", redact(err, dsn))
	}
	defer conn.Close()
	db, err := pgstore.ReadIndexDatabase(ctx, conn, p.Schema)
	if err != nil {
		return fmt.Errorf("the index's database: %w", redact(err, dsn))
	}
	version, available, canInstall, schemaExists, canCreate := db.Vector, db.VectorAvailable, db.CanInstallVector, db.SchemaExists, db.CanCreate
	const field = "postgres.index_dsn_env"
	switch {
	case version == "" && !available:
		return config.Refuse(path, field, "the index's database has no vector extension to install: it needs pgvector")
	case version == "" && !canInstall:
		return config.Refuse(path, field, "pgvector is not installed in the index's database, and this role can't install it: CREATE EXTENSION vector there once, as its owner or a superuser")
	case !canCreate && schemaExists:
		return config.Refuse(path, field, fmt.Sprintf("this role can't create tables in schema %s of the index's database: GRANT CREATE ON SCHEMA %s, or make the role its owner", p.Schema, p.Schema))
	case !canCreate:
		return config.Refuse(path, field, fmt.Sprintf("schema %s is not in the index's database, and this role can't create it: CREATE SCHEMA %s AUTHORIZATION the role, or GRANT CREATE ON DATABASE", p.Schema, p.Schema))
	case !db.CanTemp:
		// Without it the server starts and searches exactly, and the approximate search's index
		// fails to build once a tenant is large enough to want it (the status says ann-failed):
		// long after the grants were made. So it is refused here, where they are being made.
		return config.Refuse(path, field, "this role can't create temporary tables in the index's database, which building the approximate search's index needs: GRANT TEMPORARY ON DATABASE to the role (PostgreSQL grants it to every role unless it was revoked from PUBLIC)")
	}
	vector := "vector " + version
	if version == "" {
		vector = "vector installed at the first start"
	}
	schema := "schema " + p.Schema
	if !schemaExists {
		schema += " created at the first start"
	}
	fmt.Fprintf(out, "index: a database of its own: %s, %s\n", vector, schema)
	return nil
}

// redact is err with every appearance of each DSN given, and of its password, replaced: a driver's
// error may quote the connection string it failed on, and the log is not a place for a secret. It
// keeps err's identity (errors.Is and As) only when nothing was replaced.
func redact(err error, dsns ...string) error {
	if err == nil {
		return err
	}
	msg := err.Error()
	clean := msg
	for _, dsn := range dsns {
		if dsn == "" {
			continue
		}
		clean = strings.ReplaceAll(clean, dsn, "[the DSN]")
		if pw := passwordOf(dsn); pw != "" {
			clean = strings.ReplaceAll(clean, pw, "[REDACTED]")
		}
	}
	if clean == msg {
		return err
	}
	return errors.New(clean)
}

// passwordOf is the password a DSN carries: a URL's user information, or a key/value DSN's
// password= field.
func passwordOf(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		pw, _ := u.User.Password()
		return pw
	}
	for _, f := range strings.Fields(dsn) {
		if v, ok := strings.CutPrefix(f, "password="); ok {
			return strings.Trim(v, "'")
		}
	}
	return ""
}
