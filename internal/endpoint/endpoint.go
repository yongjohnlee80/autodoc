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
	"github.com/yongjohnlee80/autodoc/internal/ipc"
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
	Store    string // the store file (a SQLite store's; the default's path for a store on PostgreSQL)
	StateDir string // the daemon's log, restart handoffs, the TUI's state
	// Engine is "postgres" for a store on PostgreSQL ([store] engine), and Schema its schema: what a
	// client matches the daemon on its socket with (CheckPostgresStore). Never its DSN: a client
	// never reads the database.
	Engine, Schema string
}

// Postgres reports whether p's store is on PostgreSQL.
func (p Paths) Postgres() bool { return p.Engine == store.EngineOfPostgres }

// WithStore is p with the config's [store] engine and schema.
func (e *Endpoints) WithStore(p Paths) Paths {
	if e.Config.Store.Postgres() {
		p.Engine, p.Schema = store.EngineOfPostgres, e.Config.Store.Schema
	}
	return p
}

// StoreLocation is where the daemon's store is: the SQLite file, or the PostgreSQL database
// [store] names, its DSN read from the variable store.dsn_env names, its key in store.key_file (or
// store.key in the data directory). It is the daemon's alone: a client never reads the DSN.
func (e *Endpoints) StoreLocation() (store.Location, error) {
	path, err := e.Store()
	if err != nil {
		return store.Location{}, err
	}
	sc := e.Config.Store
	if !sc.Postgres() {
		return store.Location{Engine: store.EngineOfSQLite, Path: path}, nil
	}
	dsn := os.Getenv(sc.DSNEnv)
	if dsn == "" {
		return store.Location{}, fmt.Errorf("%w: store.dsn_env names $%s, which is not set: it holds the PostgreSQL store's DSN", config.ErrInvalid, sc.DSNEnv)
	}
	key := sc.KeyFile
	if key == "" {
		dir, err := e.DataDir()
		if err != nil {
			return store.Location{}, err
		}
		key = filepath.Join(dir, config.StoreKeyName)
	}
	return store.Location{Engine: store.EngineOfPostgres, DSN: dsn, Schema: sc.Schema, KeyFile: key}, nil
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

// DataDir is the config's data folder, absolute: the store's folder (server.data_dir when it is
// set). AutoDoc's own files for its crew agents (the OpenCode plugin, what each Claude Code knows)
// go there, never into a process's working folder: every host reads it from here.
func (e *Endpoints) DataDir() (string, error) {
	store, err := e.Store()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Dir(store))
}

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
	return e.WithStore(p), nil
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
	info, err := rpc.Probe(probeCtx, ipc.Network, li.Addr)
	if err != nil || info.StoreID != id || info.Instance != li.Instance {
		return "", false
	}
	return li.Addr, true
}

// HolderOf is Holder for the store p names: for a store on PostgreSQL, the configured socket, which
// is where its daemon serves (there is no lease-info beside it to name another).
func HolderOf(ctx context.Context, p Paths) string {
	if p.Postgres() {
		return p.Socket
	}
	return Holder(ctx, p.Store, p.Socket)
}

// VerifiedOf is Verified for the store p names: for a store on PostgreSQL, the configured socket
// when the daemon answering there says it serves that store (engine and schema).
func VerifiedOf(ctx context.Context, p Paths) (string, bool) {
	if !p.Postgres() {
		return Verified(ctx, p.Store)
	}
	if _, err := ProbePaths(ctx, p.Socket, p); err != nil {
		return "", false
	}
	return p.Socket, true
}

// ProbePaths is Probe for the store p names: a store on PostgreSQL is matched by the engine and
// schema the daemon says (CheckPostgresStore), a SQLite one by its file's identity.
func ProbePaths(ctx context.Context, addr string, p Paths) (rpc.ProbeInfo, error) {
	if !p.Postgres() {
		return Probe(ctx, addr, p.Store)
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeWait)
	defer cancel()
	info, err := rpc.Probe(probeCtx, ipc.Network, addr)
	if err != nil {
		return info, err
	}
	return info, CheckPostgresStore(addr, p.Schema, info.Engine, info.Schema)
}

// CheckPostgresStore refuses a daemon at addr that does not say it serves the store on PostgreSQL in
// schema: one serving a SQLite store, or another schema's, is a *StoreMismatchError naming both.
func CheckPostgresStore(addr, schema, engine, served string) error {
	if engine == store.EngineOfPostgres && served == schema {
		return nil
	}
	as := "a SQLite store"
	if engine == store.EngineOfPostgres {
		as = "the PostgreSQL store in schema " + served
	}
	return &StoreMismatchError{Addr: addr, StorePath: "the PostgreSQL store in schema " + schema, ServedAs: as}
}

// Probe asks the daemon at addr who it is (rpc.Probe), refusing one that serves another store than
// storePath with a *StoreMismatchError. Any other error is the probe's own: nothing answers there.
func Probe(ctx context.Context, addr, storePath string) (rpc.ProbeInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeWait)
	defer cancel()
	info, err := rpc.Probe(probeCtx, ipc.Network, addr)
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
	// ServedAs, when set, says which store the daemon serves (a store on PostgreSQL's mismatch)
	ServedAs string
}

func (e *StoreMismatchError) Error() string {
	if e.ServedAs != "" {
		return fmt.Sprintf("the daemon on %s serves %s, not %s: stop it (autodoc --call sys.shutdown with its config) or give this config its own socket",
			e.Addr, e.ServedAs, e.StorePath)
	}
	return fmt.Sprintf("the daemon on %s serves another store than %s: stop it (autodoc --call sys.shutdown with its config) or give this config its own socket",
		e.Addr, e.StorePath)
}
