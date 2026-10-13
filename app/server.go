package app

import (
	"context"
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

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/api"
	"github.com/yongjohnlee80/autodoc/enterprise/config"
	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
	"github.com/yongjohnlee80/autodoc/enterprise/ingest"
	"github.com/yongjohnlee80/autodoc/enterprise/ops"
)

// runServer is --server: the enterprise search server, headless, with no socket, no TUI and no
// SQLite. It reads server.toml at path and every source's view, refusing (a *config.Refusal, exit
// 2) anything wrong, naming the field, before it opens anything. With check, it then confirms each
// view's order against the live source, says what it checked on stdout, and returns.
//
// Otherwise it logs to stderr, and its subsystems start in serverSteps' order, each pushing how to
// stop what it started: they stop in reverse when ctx ends (SIGTERM) or an ingester fails (a lost
// lock), the embedding worker first, then the ingesters, releasing their tenants' locks, and the
// pools last.
func runServer(ctx context.Context, path string, check bool, stdout, stderr io.Writer, version string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	sources, err := loadSources(path, cfg)
	if err != nil {
		return err
	}
	if check {
		return checkServer(ctx, path, cfg, sources, stdout)
	}
	s := &server{ctx: ctx, cfg: cfg, sources: sources, log: logger.New(logger.WithWriter(stderr)), failed: make(chan error, len(sources)+1), version: version}
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

	// store is pgstore's pool, every connection in the configured schema; business reads the
	// views on the role's own search_path; locks holds one pinned connection per tenant's lock.
	store, business, locks dao.DataConn
	// failed carries the first ingester, or the HTTP server, that stopped with an error
	failed chan error
	// ingesters are the sources' ingesters, in the configuration's order: the embedding worker
	// writes a tenant only while its ingester holds the tenant's lock.
	ingesters []*ingest.Ingester
	// worker is the embedding worker once it has started, which an ingester's commit wakes; nil
	// before, when a commit has nothing to wake (the worker's first round reads what it wrote).
	worker atomic.Pointer[embedder.Worker]
	// ops tells an operator the server's health and status, and logs the metrics lines; api is the
	// HTTP API once it serves, whose counts the metrics lines take
	ops     *ops.Collector
	api     atomic.Pointer[api.Handler]
	version string
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
	{"policy", (*server).setPolicies},
	{"ingesters", (*server).startIngesters},
	{"embedder", (*server).startEmbedder},
	{"ops", (*server).startOps},
	{"http", (*server).startHTTP},
}

// openPostgres opens the three pools on the configured database. The DSN is a secret: an error
// opening it is redacted of it.
func (s *server) openPostgres() error {
	dsn := s.cfg.Postgres.DSN()
	open := func(opts ...postgres.Option) (dao.DataConn, error) {
		conn, err := postgres.OpenNamed(s.ctx, "autodoc-server", dsn, opts...)
		if err != nil {
			return nil, redact(err, dsn)
		}
		s.stops.push(func() { _ = conn.Close() })
		return conn, nil
	}
	var err error
	if s.store, err = open(pgstore.WithSchema(s.cfg.Postgres.Schema)); err != nil {
		return err
	}
	if s.business, err = open(); err != nil {
		return err
	}
	s.locks, err = open(postgres.MaxOpenConns(len(s.sources)))
	return err
}

// migrate creates the schema, then applies pgstore's migrations in it.
func (s *server) migrate() error {
	if err := pgstore.EnsureSchema(s.ctx, s.store, s.cfg.Postgres.Schema); err != nil {
		return redact(err, s.cfg.Postgres.DSN())
	}
	return redact(pgstore.Migrate(s.ctx, s.store), s.cfg.Postgres.DSN())
}

// setPolicies records each tenant's access policy from the configuration: the fields every search
// of the tenant must be scoped by, and the view it is indexed from.
func (s *server) setPolicies() error {
	for i, src := range s.cfg.Sources {
		st, err := pgstore.New(s.store, src.Tenant)
		if err != nil {
			return err
		}
		if err := st.SetPolicy(s.ctx, pgstore.Policy{AccessFields: src.Access, View: s.sources[i].Name()}); err != nil {
			return fmt.Errorf("tenant %s: %w", src.Tenant, err)
		}
	}
	return nil
}

// startIngesters runs one ingester per source until the server stops. The first that fails stops
// the server; stopping cancels them all and waits for each to release its lock.
func (s *server) startIngesters() error {
	ctx, cancel := context.WithCancel(s.ctx)
	var wg sync.WaitGroup
	s.stops.push(func() {
		cancel()
		wg.Wait()
	})
	for i, src := range s.cfg.Sources {
		st, err := pgstore.New(s.store, src.Tenant)
		if err != nil {
			return err
		}
		in := ingest.New(st, src.Tenant, s.sources[i], s.business, s.locks, ingest.Options{Schema: s.cfg.Postgres.Schema,
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
		st, err := pgstore.New(s.store, in.Tenant())
		if err != nil {
			return err
		}
		tenants[i] = embedder.Tenant{Name: in.Tenant(), Store: st, Confirm: in.Confirm}
	}
	w := embedder.New(providerOf(s.cfg.Embedding), tenants, embedder.WithSlots(s.cfg.Embedding.Slots), embedder.WithLogger(s.log))
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
// its tenant's lock: the worker leaves a tenant alone until it is this server's.
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
		src, err := ingest.Load(s.View, s.Access)
		if err != nil {
			refused.Errors = append(refused.Errors, &config.Error{Field: fmt.Sprintf("source[%d].view", i), Reason: err.Error()})
			continue
		}
		out[i] = src
	}
	if len(refused.Errors) > 0 {
		refused.Path = path
		return nil, &refused
	}
	return out, nil
}

// checkServer is --server --check, after the configuration and the views loaded: each view's order
// is confirmed against the live source, read-only, up to now − its lag, and the embedding provider
// is asked to embed a probe. It writes what it checked, and the configuration's warnings, to out.
func checkServer(ctx context.Context, path string, cfg *config.Server, sources []*ingest.Source, out io.Writer) error {
	for _, w := range cfg.Warnings {
		fmt.Fprintln(out, "warning:", w)
	}
	dsn := cfg.Postgres.DSN()
	business, err := postgres.OpenNamed(ctx, "autodoc-server", dsn)
	if err != nil {
		return redact(err, dsn)
	}
	defer business.Close()
	now, err := pgstore.Now(ctx, business)
	if err != nil {
		return redact(err, dsn)
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
	fmt.Fprintf(out, "%s: ok\n", path)
	return nil
}

// redact is err with every appearance of the DSN, and of its password, replaced: a driver's error
// may quote the connection string it failed on, and the log is not a place for a secret. It keeps
// err's identity (errors.Is and As) only when nothing was replaced.
func redact(err error, dsn string) error {
	if err == nil || dsn == "" {
		return err
	}
	msg := err.Error()
	clean := strings.ReplaceAll(msg, dsn, "[the DSN]")
	if pw := passwordOf(dsn); pw != "" {
		clean = strings.ReplaceAll(clean, pw, "[REDACTED]")
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
