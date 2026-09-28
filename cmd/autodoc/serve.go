package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/embed"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/workspace"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// errAlreadyServing is a compatible AutoDoc answering on the endpoint: this process serves nothing.
var errAlreadyServing = errors.New("autodoc is already serving")

// providerTimeout bounds the embedding provider's setup at start (its digest and a probe text).
const providerTimeout = 30 * time.Second

// runServe is --serve (ADR 0203 §3): bind the endpoint, open every workspace (its root, its store
// and the lease on it; a workspace another instance holds is served as busy), start its indexer
// and follower (the follower watches, then reconciles), and serve the API until ctx ends or a
// client says sys.shutdown. Then it drains, stops every workspace, and removes the socket if it is
// still this process's.
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
	stateDir, err := cfg.Server.StateDirPath()
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

	provider, providerFor := embedding(ctx, cfg.Embedding, log)
	wsCtx, stopWorkspaces := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var served []*rpc.Workspace
	var closers []func()
	defer func() {
		stopWorkspaces()
		wg.Wait()
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()
	for _, wc := range cfg.Workspaces {
		w, closer, err := openWorkspace(wsCtx, &wg, wc, stateDir, cfg.Follow.PollInterval.Duration, provider, providerFor, log)
		if err != nil {
			logger.Warning(log, err, "workspace not served: "+wc.Name)
			served = append(served, &rpc.Workspace{Name: wc.Name, Root: wc.Root, Err: err})
			continue
		}
		served = append(served, w)
		closers = append(closers, closer)
	}
	srv := rpc.New(served, version, rpc.WithListener(ln), rpc.WithLogger(log))
	fmt.Fprintf(out, "autodoc %s serving msgpack-RPC on %s\n", version, sock)
	return srv.Run(ctx)
}

// openWorkspace opens one workspace and starts its indexer and follower on wg; the closer releases
// its store and lease once they have stopped.
func openWorkspace(ctx context.Context, wg *sync.WaitGroup, wc config.Workspace, stateDir string, poll time.Duration,
	provider embed.Provider, providerFor func(embed.Model) (embed.Provider, error), log logger.Logger) (*rpc.Workspace, func(), error) {
	ws, err := workspace.Open(wc, stateDir)
	if err != nil {
		return nil, nil, err
	}
	store, err := index.Open(ctx, ws.IndexPath())
	if err != nil {
		_ = ws.Close()
		return nil, nil, fmt.Errorf("workspace %q: %w", wc.Name, err)
	}
	ix := index.NewIndexer(store, ws.FS, index.Options{Match: ws.Matcher.Match, Provider: provider, ProviderFor: providerFor})
	f := follow.New(ws.FS, ix, ix, follow.Options{PollInterval: poll, Match: ws.Matcher.Match, Excluded: ws.Matcher.Excluded})
	ix.SetRescanner(f) // index.reindex(ws, "") finds the files the index lacks through the follower
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := ix.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error(log, err, "indexer stopped: "+wc.Name)
		}
	}()
	go func() {
		defer wg.Done()
		_ = f.Run(ctx)
	}()
	closer := func() {
		_ = store.Close()
		_ = ws.Close()
	}
	return &rpc.Workspace{Name: wc.Name, Root: wc.Root, Index: ix, Docs: docs.New(ws.FS, ws.Matcher.Match), Following: f.Status}, closer, nil
}

// embedding makes the configured provider, and the maker of the providers of other models of the
// same kind (the one still active while a new model fills). A provider that cannot be set up is
// logged, and search is lexical: semantic search waits for a restart with a working provider.
func embedding(ctx context.Context, e config.Embedding, log logger.Logger) (embed.Provider, func(embed.Model) (embed.Provider, error)) {
	if e.Provider == "" {
		return nil, nil
	}
	key, err := e.APIKey()
	if err != nil {
		logger.Warning(log, err, "embedding off")
		return nil, nil
	}
	client := &http.Client{}
	newProvider := func(ctx context.Context, name string) (embed.Provider, error) {
		ctx, cancel := context.WithTimeout(ctx, providerTimeout)
		defer cancel()
		if e.Provider == config.EmbedOllama {
			return embed.NewOllama(ctx, e.BaseURL, name, client)
		}
		return embed.NewOpenAI(ctx, e.BaseURL, key, name, client)
	}
	p, err := newProvider(ctx, e.Model)
	if err != nil {
		logger.Warning(log, err, "embedding off: the provider could not be set up")
		return nil, nil
	}
	return p, func(m embed.Model) (embed.Provider, error) {
		if m.Provider != p.Model().Provider {
			return nil, fmt.Errorf("model %s is not the configured provider's", m.Fingerprint())
		}
		return newProvider(context.Background(), m.Name)
	}
}
