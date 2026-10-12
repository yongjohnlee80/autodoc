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

	"github.com/yongjohnlee80/autodoc/enterprise/api"
	"github.com/yongjohnlee80/autodoc/enterprise/config"
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
	w := s.worker.Load()
	tenants := make(map[string]api.Searcher, len(s.cfg.Sources))
	for _, src := range s.cfg.Sources {
		sr, err := search.Open(s.ctx, s.store, src.Tenant, w.QueryEmbedder(src.Tenant))
		if err != nil {
			return redact(err, s.cfg.Postgres.DSN())
		}
		tenants[src.Tenant] = sr
	}
	h := api.New(clients, tenants, api.WithLogger(s.log))
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
	s.stops.push(func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Warning(s.log, err, "http: requests still running past the grace are closed")
			_ = srv.Close()
		}
		h.Wait()
		<-served
	})
	logger.Info(s.log, fmt.Sprintf("search API on %s (%d clients)", ln.Addr(), len(clients)))
	return nil
}

// resolveClients is the configured clients as the API compares them: each token's SHA-256, from
// token_sha256 or from the token token_env names. The configuration's load checked their form; an
// environment variable emptied since is refused here, naming the client, never the token.
func resolveClients(cfg []config.Client) ([]api.Client, error) {
	out := make([]api.Client, len(cfg))
	for i, c := range cfg {
		out[i] = api.Client{Name: c.Name, Grants: c.Grants}
		switch {
		case c.TokenSHA256 != "":
			b, err := hex.DecodeString(c.TokenSHA256)
			if err != nil || len(b) != sha256.Size {
				return nil, fmt.Errorf("http client %s: token_sha256 is not a SHA-256 in hex", c.Name)
			}
			copy(out[i].Token[:], b)
		default:
			tok := os.Getenv(c.TokenEnv)
			if tok == "" {
				return nil, fmt.Errorf("http client %s: %s is not set", c.Name, c.TokenEnv)
			}
			out[i].Token = sha256.Sum256([]byte(tok))
		}
	}
	return out, nil
}
