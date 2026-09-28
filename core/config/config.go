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
//	[embedding]                 # optional: without it, search is lexical
//	provider = "ollama"         # "ollama", or "openai" for any OpenAI-compatible endpoint
//	model = "snowflake-arctic-embed" # e.g.: the model the provider serves (no default)
//	base_url = ""               # default: http://localhost:11434 (ollama), https://api.openai.com (openai)
//	api_key_env = ""            # openai: the environment variable holding the key (never the key itself)
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
	"net/url"
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
	Embedding  Embedding   `toml:"embedding"`
	Workspaces []Workspace `toml:"workspace"`
}

// Embedding names the embedding provider semantic search uses. The zero value is none.
type Embedding struct {
	Provider string `toml:"provider"` // "" (none), EmbedOllama or EmbedOpenAI
	Model    string `toml:"model"`
	BaseURL  string `toml:"base_url"`
	// APIKeyEnv names the environment variable holding an OpenAI-compatible endpoint's key. The key
	// itself is never in the file, which is often shared or kept in git.
	APIKeyEnv string `toml:"api_key_env"`
}

// The embedding providers.
const (
	EmbedOllama = "ollama"
	EmbedOpenAI = "openai"
)

// The providers' default endpoints.
const (
	DefaultOllamaURL = "http://localhost:11434"
	DefaultOpenAIURL = "https://api.openai.com"
)

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
			if keys[i] == "embedding.api_key" {
				return nil, fmt.Errorf("%w: %s: embedding.api_key: a key does not belong in the file; name the environment variable holding it with api_key_env", ErrInvalid, file)
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
	if c.Follow.PollInterval.Duration == 0 {
		c.Follow.PollInterval.Duration = DefaultPollInterval
	}
	if c.Follow.PollInterval.Duration < 0 {
		return fmt.Errorf("%w: follow.poll_interval must be positive", ErrInvalid)
	}
	if err := c.Embedding.normalize(); err != nil {
		return err
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

func (e *Embedding) normalize() error {
	switch e.Provider {
	case "":
		if *e != (Embedding{}) {
			return fmt.Errorf("%w: embedding: settings with no provider", ErrInvalid)
		}
		return nil
	case EmbedOllama:
		if e.BaseURL == "" {
			e.BaseURL = DefaultOllamaURL
		}
		if e.APIKeyEnv != "" {
			return fmt.Errorf("%w: embedding.api_key_env is for the openai provider", ErrInvalid)
		}
	case EmbedOpenAI:
		if e.BaseURL == "" {
			e.BaseURL = DefaultOpenAIURL
		}
	default:
		return fmt.Errorf("%w: embedding.provider %q: want %q or %q", ErrInvalid, e.Provider, EmbedOllama, EmbedOpenAI)
	}
	if e.Model == "" {
		return fmt.Errorf("%w: embedding.model is required with a provider", ErrInvalid)
	}
	if u, err := url.Parse(e.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: embedding.base_url %q: want an http or https URL", ErrInvalid, e.BaseURL)
	}
	return nil
}

// APIKey is the provider's key, read from the environment variable APIKeyEnv names: "" when it
// names none. A named variable that is unset or empty is an error, so a missing key is reported at
// start rather than as every request failing.
func (e Embedding) APIKey() (string, error) {
	if e.APIKeyEnv == "" {
		return "", nil
	}
	k := os.Getenv(e.APIKeyEnv)
	if k == "" {
		return "", fmt.Errorf("%w: embedding.api_key_env: $%s is not set", ErrInvalid, e.APIKeyEnv)
	}
	return k, nil
}
