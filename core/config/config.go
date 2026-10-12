// Package config reads AutoDoc's configuration file and resolves the places AutoDoc keeps things:
// the endpoint the daemon listens on, the state directory, and the store. Workspaces are not
// configured here: they are rows of the store, added in the TUI or with workspace.add.
//
//	[server]
//	socket = ""                 # default: $XDG_RUNTIME_DIR/autodoc.sock
//	state_dir = ""              # default: $XDG_STATE_HOME/autodoc
//	data_dir = ""               # default: $XDG_DATA_HOME/autodoc (the store: autodoc.db)
//
//	[follow]
//	poll_interval = "2s"        # the watch fallback's listing interval
//	[embedding_queue]
//	max_inflight = 1           # requests to the shared provider, 1 or 2
//
// Nor are the embedding providers: they are the store's too, with their keys sealed, added and
// chosen in the TUI's Preferences.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/golib/errs"
)

// ErrInvalid is a configuration that cannot be used; the message says which setting and why.
var ErrInvalid = errs.Sentinel(errs.ErrInvalidArgument, "config: invalid")

// Config is the whole file, with defaults filled in.
type Config struct {
	Server         Server         `toml:"server"`
	Follow         Follow         `toml:"follow"`
	EmbeddingQueue EmbeddingQueue `toml:"embedding_queue"`
}

// EmbeddingQueue bounds concurrent provider calls across every workspace.
type EmbeddingQueue struct {
	MaxInflight int `toml:"max_inflight"`
}

// Server is where the daemon listens and keeps its state.
type Server struct {
	Socket   string `toml:"socket"`    // the unix socket; "" resolves to the default
	StateDir string `toml:"state_dir"` // the state directory; "" resolves to the default
	DataDir  string `toml:"data_dir"`  // the store's directory; "" resolves to the default
}

// Follow configures how workspaces follow their roots.
type Follow struct {
	PollInterval Duration `toml:"poll_interval"`
}

// Duration is a time.Duration written as a string ("2s", "500ms").
type Duration struct{ time.Duration }

// UnmarshalText reads a duration string.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// DefaultPollInterval is the watch fallback's listing interval when none is configured.
const DefaultPollInterval = 2 * time.Second

// DefaultPath is where the configuration file lives: $XDG_CONFIG_HOME/autodoc/config.toml, else
// ~/.config/autodoc/config.toml.
func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("%w: no XDG_CONFIG_HOME and no home directory", ErrInvalid)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "autodoc", "config.toml"), nil
}

// Load reads and validates the configuration file at file. A missing file is not an error: every
// value is a default (and there is no workspace until one is configured), as AutoDB's is. A file
// that cannot be read is the machine's problem, not the configuration's; one that does not parse,
// or names an unknown setting, is invalid, so a misspelled setting is reported instead of ignored.
func Load(file string) (*Config, error) {
	var c Config
	md, err := toml.DecodeFile(file, &c)
	var pe *fs.PathError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c = Config{}
		if err := c.normalize(); err != nil {
			return nil, err
		}
		return &c, nil
	case errors.As(err, &pe):
		return nil, fmt.Errorf("config: %s: %w", file, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, file, err)
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
			if keys[i] == "embedding" || strings.HasPrefix(keys[i], "embedding.") {
				return nil, fmt.Errorf("%w: %s: [embedding]: embedding providers are kept in the store now, their keys sealed; add them in the TUI's Preferences (autodoc --ui, then SPC ,), and remove the section", ErrInvalid, file)
			}
			if keys[i] == "workspace" || strings.HasPrefix(keys[i], "workspace.") {
				return nil, fmt.Errorf("%w: %s: [[workspace]]: workspaces are kept in the store now; add them in the TUI's workspace manager (autodoc --ui, then w) or with workspace.add, and remove the section", ErrInvalid, file)
			}
		}
		return nil, fmt.Errorf("%w: %s: unknown settings %s", ErrInvalid, file, strings.Join(keys, ", "))
	}
	if err := c.normalize(); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return &c, nil
}

// normalize fills defaults, expands roots, and checks what can be checked without the filesystem.
func (c *Config) normalize() error {
	if c.EmbeddingQueue.MaxInflight == 0 {
		c.EmbeddingQueue.MaxInflight = 1
	}
	if c.EmbeddingQueue.MaxInflight < 1 || c.EmbeddingQueue.MaxInflight > 2 {
		return fmt.Errorf("%w: embedding_queue.max_inflight must be 1 or 2", ErrInvalid)
	}
	if c.Follow.PollInterval.Duration == 0 {
		c.Follow.PollInterval.Duration = DefaultPollInterval
	}
	if c.Follow.PollInterval.Duration < 0 {
		return fmt.Errorf("%w: follow.poll_interval must be positive", ErrInvalid)
	}
	return nil
}

// SocketName is the default endpoint's file name.
const SocketName = "autodoc.sock"

// maxSocketPath bounds a unix socket path below the kernels' sockaddr_un limits (104 bytes on
// Darwin, 108 on Linux), so a long path fails here with its length rather than at bind with EINVAL.
const maxSocketPath = 100

// SocketPath resolves the unix socket the daemon listens on and clients dial.
func (s Server) SocketPath() (string, error) {
	p := s.Socket
	if p == "" {
		dir, err := runtimeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(dir, SocketName)
	}
	if len(p) > maxSocketPath {
		return "", fmt.Errorf("%w: socket path %q is %d bytes, over the %d-byte limit — set server.socket to something shorter", ErrInvalid, p, len(p), maxSocketPath)
	}
	return p, nil
}

// runtimeDir is where the socket goes: $XDG_RUNTIME_DIR, then the per-user $TMPDIR on Darwin, then
// the state directory.
func runtimeDir() (string, error) {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "darwin" {
		if d := os.TempDir(); d != "" {
			return d, nil
		}
	}
	return Server{}.StateDirPath()
}

// StateDirPath resolves the state directory: server.state_dir, else $XDG_STATE_HOME/autodoc, else
// ~/.local/state/autodoc. The daemon's log lives here. It creates nothing: a mode that writes there
// makes it (internal/endpoint).
func (s Server) StateDirPath() (string, error) {
	return xdgDir(s.StateDir, "XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// StoreName is the store's file name in the data directory.
const StoreName = "autodoc.db"

// StorePath resolves the store: server.data_dir, else $XDG_DATA_HOME/autodoc, else
// ~/.local/share/autodoc, and autodoc.db in it. It creates nothing: a mode that opens the store
// makes its directory (internal/endpoint). The store holds the
// workspaces, which nothing rebuilds, so it is data rather than state; it is never in a root.
func (s Server) StorePath() (string, error) {
	dir, err := xdgDir(s.DataDir, "XDG_DATA_HOME", filepath.Join(".local", "share"))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, StoreName), nil
}

// xdgDir is a configured directory, else $env/autodoc, else ~/fallback/autodoc.
func xdgDir(configured, env, fallback string) (string, error) {
	dir := configured
	if dir == "" {
		base := os.Getenv(env)
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("%w: no %s and no home directory", ErrInvalid, env)
			}
			base = filepath.Join(home, fallback)
		}
		dir = filepath.Join(base, "autodoc")
	} else {
		var err error
		if dir, err = paths.ExpandHome(dir); err != nil {
			return "", fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	return dir, nil
}
