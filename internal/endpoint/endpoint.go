// Package endpoint finds the daemon a config's clients talk to: the socket the config names, or,
// when another daemon already serves the same store on another socket, that one. Every client
// resolves through it before it connects (the TUI, --call, --print-endpoint), so two configs that
// share a store attach to one daemon instead of spawning into its lease. Load is every mode's
// prologue: a config's paths, read once.
package endpoint

import (
	"context"
	"errors"
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

// Endpoints are a config, loaded once, and the paths it names: where its daemon serves, its store,
// and its state directory. Each path is resolved when a mode asks for it, so a mode never fails on
// a path it does not use; a mode that writes in one makes its directory (MakeStateDir, MakeDataDir).
type Endpoints struct {
	ConfigPath string // the config file, the default's resolved
	Config     *config.Config
}

// Paths are the three paths a mode that runs or starts the daemon uses.
type Paths struct {
	Socket   string // the configured socket, where a spawned daemon binds
	Store    string // the store file
	StateDir string // the daemon's log, restart handoffs, the TUI's state
}

// Load reads the config at configPath ("" for config.DefaultPath).
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
	return &Endpoints{ConfigPath: configPath, Config: cfg}, nil
}

// Socket is the configured socket.
func (e *Endpoints) Socket() (string, error) { return e.Config.Server.SocketPath() }

// Store is the store file.
func (e *Endpoints) Store() (string, error) { return e.Config.Server.StorePath() }

// StateDir is the state directory.
func (e *Endpoints) StateDir() (string, error) { return e.Config.Server.StateDirPath() }

// Paths resolves all three, for a mode that serves, spawns or restarts the daemon.
func (e *Endpoints) Paths() (Paths, error) {
	var p Paths
	var err error
	if p.StateDir, err = e.StateDir(); err != nil {
		return Paths{}, err
	}
	if p.Socket, err = e.Socket(); err != nil {
		return Paths{}, err
	}
	if p.Store, err = e.Store(); err != nil {
		return Paths{}, err
	}
	return p, nil
}

// MakeStateDir creates the state directory, 0700, for a mode that writes in it.
func (e *Endpoints) MakeStateDir() error {
	dir, err := e.StateDir()
	if err != nil {
		return err
	}
	return makeDir(dir)
}

// MakeDataDir creates the store's directory, 0700, for a mode that opens the store, and has
// SQLite's temporary files go beside it (TempBeside).
func (e *Endpoints) MakeDataDir() error {
	p, err := e.Store()
	if err != nil {
		return err
	}
	TempBeside(p)
	return makeDir(filepath.Dir(p))
}

// TempBeside makes SQLite's temporary files (a VACUUM's copy among them) go beside the store at
// path, never to the system's temp directory, which may be memory (tmpfs). SQLite's unix VFS reads
// SQLITE_TMPDIR; the driver's C library takes its copy of the environment at the process's first
// connection, so a mode sets it before it opens the store, and it is never changed under a running
// connection (the deprecated temp_store_directory pragma would, process-wide). A value the user set
// is kept. It is the process's, which is why core/store, which only opens the store, does not set it.
func TempBeside(path string) {
	if os.Getenv("SQLITE_TMPDIR") == "" {
		_ = os.Setenv("SQLITE_TMPDIR", filepath.Dir(path))
	}
}

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
	return info, CheckStore(addr, storePath, info.StoreID, info.Protocol)
}

// storeIDSince is the protocol from which every daemon says its store_id in its hello.
const storeIDSince = 13

// ErrNoStoreID is a daemon whose protocol says its store_id and whose hello leaves it out: it is
// not a daemon of this build's that serves a store, and nothing it would answer can be trusted to
// be this store's.
var ErrNoStoreID = errors.New("the daemon does not say which store it serves")

// CheckStore refuses a daemon at addr that says it serves the store served, when that is not the
// store at storePath. protocol is the daemon's own (its hello's server_protocol, or its probe's
// protocol). A daemon too old to say its store_id (protocol below 13) is let through: nothing can
// be checked. A newer one that says none is refused (ErrNoStoreID).
func CheckStore(addr, storePath, served string, protocol int64) error {
	if served == "" {
		if protocol >= storeIDSince {
			return fmt.Errorf("%s: %w", addr, ErrNoStoreID)
		}
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
