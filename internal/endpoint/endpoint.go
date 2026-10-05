// Package endpoint finds the daemon a config's clients talk to: the socket the config names, or,
// when another daemon already serves the same store on another socket, that one. Every client
// resolves through it before it connects (the TUI, --call, --print-endpoint), so two configs that
// share a store attach to one daemon instead of spawning into its lease.
package endpoint

import (
	"context"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// probeWait bounds the probe of the address a lease-info names.
const probeWait = 2 * time.Second

// Resolve is the address a client of cfg connects to, and the one cfg names (where a spawned daemon
// binds). The store's lease-info is believed only when the address it names answers as this
// store's daemon (store_id) and as the process that wrote the record (instance); a missing, stale or
// mismatched record leaves the configured socket.
func Resolve(ctx context.Context, cfg *config.Config) (addr, configured string, err error) {
	configured, err = cfg.Server.SocketPath()
	if err != nil {
		return "", "", err
	}
	storePath, err := cfg.Server.StorePath()
	if err != nil {
		return configured, configured, nil
	}
	return Holder(ctx, storePath, configured), configured, nil
}

// Holder is the address of the daemon serving the store at storePath, as its lease-info names it and
// a probe confirms, or fallback.
func Holder(ctx context.Context, storePath, fallback string) string {
	id, err := store.Identity(storePath)
	if err != nil {
		return fallback
	}
	li, err := store.ReadLeaseInfo(storePath)
	if err != nil || li.Addr == "" || li.StoreID != id || li.Addr == fallback {
		return fallback
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeWait)
	defer cancel()
	info, err := rpc.Probe(probeCtx, "unix", li.Addr)
	if err != nil || info.StoreID != id || info.Instance != li.Instance {
		return fallback
	}
	return li.Addr
}
