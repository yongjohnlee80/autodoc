// Package endpoint finds the daemon a config's clients talk to: the socket the config names, or,
// when another daemon already serves the same store on another socket, that one. Every client
// resolves through it before it connects (the TUI, --call, --print-endpoint), so two configs that
// share a store attach to one daemon instead of spawning into its lease. Load is every mode's
// prologue: a config's paths, read once.
package endpoint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// probeWait bounds the probe of the address a lease-info names, and of a socket's occupant.
const probeWait = 2 * time.Second

// Endpoints are a config's paths: where its daemon serves, its store, and its state directory.
// Load only resolves them; a mode that writes one makes its directory (MakeStateDir, MakeDataDir).
type Endpoints struct {
	ConfigPath string // the config file, the default's resolved
	Config     *config.Config
	Socket     string // the configured socket, where a spawned daemon binds
	Store      string // the store file
	StateDir   string // the daemon's log, restart handoffs, the TUI's state
}

// Load reads the config at configPath ("" for config.DefaultPath) and resolves its paths.
func Load(configPath string) (*Endpoints, error) {
	if configPath == "" {
		var err error
		if configPath, err = config.DefaultPath(); err != nil {
			return nil, err
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	e := &Endpoints{ConfigPath: configPath, Config: cfg}
	if e.StateDir, err = cfg.Server.StateDirPath(); err != nil {
		return nil, err
	}
	if e.Socket, err = cfg.Server.SocketPath(); err != nil {
		return nil, err
	}
	if e.Store, err = cfg.Server.StorePath(); err != nil {
		return nil, err
	}
	return e, nil
}

// MakeStateDir creates the state directory, 0700, for a mode that writes in it.
func (e *Endpoints) MakeStateDir() error { return makeDir(e.StateDir) }

// MakeDataDir creates the store's directory, 0700, for a mode that opens the store.
func (e *Endpoints) MakeDataDir() error { return makeDir(filepath.Dir(e.Store)) }

func makeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	return nil
}

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
// a probe confirms, or fallback. A lease-info naming fallback itself is not probed.
func Holder(ctx context.Context, storePath, fallback string) string {
	if addr, ok := holder(ctx, storePath, fallback); ok {
		return addr
	}
	return fallback
}

// Verified is the address of the daemon holding the store's lease, when its lease-info names one
// that answers as this store's daemon and as the process the record was written by.
func Verified(ctx context.Context, storePath string) (string, bool) {
	return holder(ctx, storePath, "")
}

func holder(ctx context.Context, storePath, skip string) (string, bool) {
	id, err := store.Identity(storePath)
	if err != nil {
		return "", false
	}
	li, err := store.ReadLeaseInfo(storePath)
	if err != nil || li.Addr == "" || li.StoreID != id || li.Addr == skip {
		return "", false
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeWait)
	defer cancel()
	info, err := rpc.Probe(probeCtx, "unix", li.Addr)
	if err != nil || info.StoreID != id || info.Instance != li.Instance {
		return "", false
	}
	return li.Addr, true
}

// Probe asks the daemon at addr who it is (rpc.Probe), refusing one that serves another store than
// storePath with a *StoreMismatchError. Any other error is the probe's own: nothing answers there.
func Probe(ctx context.Context, addr, storePath string) (rpc.ProbeInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeWait)
	defer cancel()
	info, err := rpc.Probe(probeCtx, "unix", addr)
	if err != nil {
		return info, err
	}
	return info, CheckStore(addr, storePath, info.StoreID)
}

// CheckStore refuses a daemon at addr that says it serves the store served, when that is not the
// store at storePath. A daemon too old to say its store_id ("") is let through: nothing can be
// checked.
func CheckStore(addr, storePath, served string) error {
	if served == "" {
		return nil
	}
	wanted, _ := store.Identity(storePath)
	if served == wanted {
		return nil
	}
	return &StoreMismatchError{Addr: addr, StorePath: storePath, Served: served, Wanted: wanted}
}

// StoreMismatchError is a daemon answering on a config's socket for another store than the config
// names: a config that changed its data_dir but kept its socket would otherwise read and write the
// old store. Nothing is spawned over it; the user stops that daemon or names another socket.
type StoreMismatchError struct {
	Addr, StorePath string
	Served, Wanted  string // store ids; Wanted "" when the config's store does not exist yet
}

func (e *StoreMismatchError) Error() string {
	return fmt.Sprintf("the daemon on %s serves another store than %s: stop it (autodoc --call sys.shutdown with its config) or give this config its own socket",
		e.Addr, e.StorePath)
}
