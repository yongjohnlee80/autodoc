// Package config reads AutoDoc's configuration file and resolves the places AutoDoc keeps things:
// the endpoint the daemon listens on, the state directory, and each workspace's root.
//
//	[server]
//	socket = ""                 # default: $XDG_RUNTIME_DIR/autodoc.sock
//	state_dir = ""              # default: $XDG_STATE_HOME/autodoc
//
//	[follow]
//	poll_interval = "2s"        # the watch fallback's listing interval
//
//	[[workspace]]
//	name = "kb"                 # the handle every API call names
//	root = "~/notes"            # one root per workspace
//	include = ["**/*.md"]       # default
//	exclude = [".git/**"]       # default
package config

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ErrInvalid is a configuration that cannot be used; the message says which setting and why.
var ErrInvalid = errors.New("config: invalid")

// Config is the whole file, with defaults filled in.
type Config struct {
	Server     Server      `toml:"server"`
	Follow     Follow      `toml:"follow"`
	Workspaces []Workspace `toml:"workspace"`
}

// Server is where the daemon listens and keeps its state.
type Server struct {
	Socket   string `toml:"socket"`    // the unix socket; "" resolves to the default
	StateDir string `toml:"state_dir"` // the state directory; "" resolves to the default
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

// Workspace is one root AutoDoc indexes.
type Workspace struct {
	Name    string   `toml:"name"`
	Root    string   `toml:"root"`
	Include []string `toml:"include"`
	Exclude []string `toml:"exclude"`
}

// The defaults a workspace gets when it names no patterns of its own.
var (
	DefaultInclude = []string{"**/*.md"}
	DefaultExclude = []string{".git/**"}
)

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

// Load reads and validates the configuration file at file. Unknown keys are an error, so a
// misspelled setting is reported instead of silently ignored.
func Load(file string) (*Config, error) {
	var c Config
	md, err := toml.DecodeFile(file, &c)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, file, err)
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
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
	if c.Follow.PollInterval.Duration == 0 {
		c.Follow.PollInterval.Duration = DefaultPollInterval
	}
	if c.Follow.PollInterval.Duration < 0 {
		return fmt.Errorf("%w: follow.poll_interval must be positive", ErrInvalid)
	}
	seen := map[string]bool{}
	for i := range c.Workspaces {
		w := &c.Workspaces[i]
		if w.Name == "" || strings.ContainsAny(w.Name, `/\`) || w.Name == "." || w.Name == ".." {
			return fmt.Errorf("%w: workspace %d: a name is required, and may not hold a path separator", ErrInvalid, i+1)
		}
		if seen[w.Name] {
			return fmt.Errorf("%w: workspace %q is defined twice", ErrInvalid, w.Name)
		}
		seen[w.Name] = true
		root, err := expandHome(w.Root)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(root) {
			return fmt.Errorf("%w: workspace %q: root %q must be absolute (or start with ~/)", ErrInvalid, w.Name, w.Root)
		}
		w.Root = filepath.Clean(root)
		if w.Include == nil {
			w.Include = DefaultInclude
		}
		if w.Exclude == nil {
			w.Exclude = DefaultExclude
		}
		for _, p := range append(append([]string{}, w.Include...), w.Exclude...) {
			if err := ValidPattern(p); err != nil {
				return fmt.Errorf("%w: workspace %q: pattern %q: %v", ErrInvalid, w.Name, p, err)
			}
		}
	}
	return nil
}

func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: %q needs a home directory", ErrInvalid, p)
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

// ValidPattern checks a root-relative glob: segments separated by '/', each a path.Match pattern
// or "**" (any number of whole segments).
func ValidPattern(p string) error {
	if p == "" || strings.HasPrefix(p, "/") {
		return errors.New("a pattern is relative to the root and may not be empty")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, ""); err != nil {
			return err
		}
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

// StateDirPath resolves the state directory, creating it 0700: server.state_dir, else
// $XDG_STATE_HOME/autodoc, else ~/.local/state/autodoc. Indexes live here, never in a root.
func (s Server) StateDirPath() (string, error) {
	dir := s.StateDir
	if dir == "" {
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("%w: no XDG_STATE_HOME and no home directory for the state", ErrInvalid)
			}
			base = filepath.Join(home, ".local", "state")
		}
		dir = filepath.Join(base, "autodoc")
	} else {
		var err error
		if dir, err = expandHome(dir); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%w: creating %s: %v", ErrInvalid, dir, err)
	}
	return dir, nil
}
