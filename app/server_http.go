package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/api"
	"github.com/yongjohnlee80/autodoc/enterprise/config"
	"github.com/yongjohnlee80/autodoc/enterprise/ops"
	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// The HTTP server's own timeouts; a request's budget is the API's (api.DefaultBudget).
//
// NOTE: deadlines and a grace, not waits. A client that never finishes its headers, or keeps an idle
// connection, must not hold it for good (headerTimeout, idleTimeout). A server stopping lets the
// requests in flight finish within their budget: its grace outlasts the longest one, so none is
// cut off by a stop alone.
const (
	headerTimeout = 5 * time.Second
	idleTimeout   = 60 * time.Second
	shutdownGrace = 35 * time.Second // more than api.DefaultBudget.Write
)

// startHTTP serves the search API when [http] is configured: one Searcher per tenant, its queries
// embedded by the embedding worker in its slots. Stopping it stops taking requests, waits for those
// in flight (within shutdownGrace, then closing them), and returns only once every handler has, so
// the worker and the pools it uses outlive every request.
func (s *server) startHTTP() error {
	if s.cfg.HTTP == nil {
		return nil
	}
	clients, err := resolveClients(s.cfg.HTTP.Clients)
	if err != nil {
		return err
	}
	operators, err := resolveOperators(s.cfg.HTTP.Operators)
	if err != nil {
		return err
	}
	w := s.worker.Load()
	tenants := make(map[string]api.Searcher, len(s.cfg.Sources))
	for _, src := range s.cfg.Sources {
		sr, err := search.Open(s.ctx, s.store, src.Tenant, w.QueryEmbedder(src.Tenant))
		if err != nil {
			return redact(err, s.cfg.Postgres.DSN())
		}
		tenants[src.Tenant] = sr
	}
	h := api.New(clients, tenants, api.WithLogger(s.log), api.WithOps(s.ops), api.WithOperators(operators))
	ln, err := net.Listen("tcp", s.cfg.HTTP.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: headerTimeout,
		IdleTimeout:       idleTimeout,
		// a request's context is not the server's: a stop drains it rather than cancelling it
		BaseContext: func(net.Listener) context.Context { return context.Background() },
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		var err error
		if s.cfg.HTTP.TLSCert != "" {
			err = srv.ServeTLS(ln, s.cfg.HTTP.TLSCert, s.cfg.HTTP.TLSKey)
		} else {
			err = srv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.failed <- fmt.Errorf("http: %w", err)
		}
	}()
	s.api.Store(h)
	s.stops.push(func() {
		s.ops.Stop() // health says unavailable while the requests in flight drain
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Warning(s.log, err, "http: requests still running past the grace are closed")
			_ = srv.Close()
		}
		h.Wait()
		<-served
	})
	logger.Info(s.log, fmt.Sprintf("search API on %s (%d clients, %d operators)", ln.Addr(), len(clients), len(operators)))
	return nil
}

// resolveClients is the configured clients as the API compares them: each token's SHA-256.
func resolveClients(cfg []config.Client) ([]api.Client, error) {
	out := make([]api.Client, len(cfg))
	for i, c := range cfg {
		tok, err := tokenOf("http client "+c.Name, c.TokenSHA256, c.TokenEnv)
		if err != nil {
			return nil, err
		}
		out[i] = api.Client{Name: c.Name, Token: tok, Grants: c.Grants}
	}
	return out, nil
}

// resolveOperators is the configured operators as the API compares them: each token's SHA-256.
func resolveOperators(cfg []config.Operator) ([]api.Operator, error) {
	out := make([]api.Operator, len(cfg))
	for i, o := range cfg {
		tok, err := tokenOf("http operator "+o.Name, o.TokenSHA256, o.TokenEnv)
		if err != nil {
			return nil, err
		}
		out[i] = api.Operator{Name: o.Name, Token: tok, Tenants: o.Tenants}
	}
	return out, nil
}

// tokenOf is a token's SHA-256, from token_sha256 or from the token token_env names. The
// configuration's load checked their form; an environment variable emptied since is refused here,
// naming who holds it (who), never the token.
func tokenOf(who, sha, env string) ([sha256.Size]byte, error) {
	var out [sha256.Size]byte
	if sha != "" {
		b, err := hex.DecodeString(sha)
		if err != nil || len(b) != sha256.Size {
			return out, fmt.Errorf("%s: token_sha256 is not a SHA-256 in hex", who)
		}
		copy(out[:], b)
		return out, nil
	}
	tok := os.Getenv(env)
	if tok == "" {
		return out, fmt.Errorf("%s: %s is not set", who, env)
	}
	return sha256.Sum256([]byte(tok)), nil
}

// startOps builds what tells an operator the server's health and status, over every tenant's store
// and its ingester's state and the embedding worker's health, and logs the metrics lines every
// [log] metrics_interval (none when it is 0) until the server stops.
func (s *server) startOps() error {
	tenants := make([]ops.Tenant, len(s.ingesters))
	for i, in := range s.ingesters {
		st, err := pgstore.New(s.store, in.Tenant())
		if err != nil {
			return err
		}
		tenants[i] = ops.Tenant{Name: in.Tenant(), Store: st, State: in.State}
	}
	s.ops = ops.New(ops.Options{Tenants: tenants, Provider: s.worker.Load().Health,
		Now: func(ctx context.Context) (time.Time, error) { return pgstore.Now(ctx, s.store) }, Version: s.version, Started: time.Now()})
	if every := s.cfg.Log.MetricsInterval; every > 0 {
		ctx, cancel := context.WithCancel(s.ctx)
		done := make(chan struct{})
		s.stops.push(func() {
			cancel()
			<-done
		})
		go func() {
			defer close(done)
			s.ops.Report(ctx, every, s.log, s.httpCounts)
		}()
	}
	return nil
}

// httpCounts are the API's requests since the last metrics lines; none before the API serves, or
// with no [http].
func (s *server) httpCounts() ops.HTTPCounts {
	if h := s.api.Load(); h != nil {
		return h.TakeCounts()
	}
	return ops.HTTPCounts{}
}
