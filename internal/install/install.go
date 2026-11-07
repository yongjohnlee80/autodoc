// Package install reads the record install-autodoc.sh writes when it installs AutoDoc
// (docs/decisions/install.md#the-install-record): which version, and where the binary and update.sh
// are. AutoDoc reads it and never writes it; an AutoDoc installed another way has none, and offers
// no Update.
//
// Concurrency: the package is functions of their arguments and the environment; any number of
// goroutines may call them.
package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Record is install.toml: what the script installed, and where.
type Record struct {
	Method      string `toml:"method"`       // "script" when install-autodoc.sh installed AutoDoc
	Version     string `toml:"version"`      // the version it installed, "v0.3.12"
	Channel     string `toml:"channel"`      // "stable"
	Bin         string `toml:"bin"`          // the binary it installed
	Launcher    string `toml:"launcher"`     // the desktop entry or AutoDoc.app; "" for none
	Update      string `toml:"update"`       // update.sh, beside the record
	InstalledAt string `toml:"installed_at"` // RFC 3339, UTC
	UpdatedAt   string `toml:"updated_at"`   // RFC 3339, UTC
}

// Scripted reports whether the script installed AutoDoc and left an update.sh: an Update can run.
func (r Record) Scripted() bool { return r.Method == "script" && r.Update != "" }

// Path is the record's path: $XDG_STATE_HOME/autodoc/install.toml, else
// ~/.local/state/autodoc/install.toml, as the scripts write it. AutoDoc's own state_dir setting
// doesn't move it: the scripts don't read AutoDoc's config.
func Path() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "autodoc", "install.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("install: the record's path: %w", err)
	}
	return filepath.Join(home, ".local", "state", "autodoc", "install.toml"), nil
}

// Read reads the record at path. ok is false, with no error, when there is none: AutoDoc was
// installed another way. A record that doesn't parse is an error.
func Read(path string) (r Record, ok bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("install: %w", err)
	}
	if err := toml.Unmarshal(b, &r); err != nil {
		return Record{}, false, fmt.Errorf("install: %s: %w", path, err)
	}
	return r, true, nil
}

// Current reads the record at Path.
func Current() (Record, bool, error) {
	p, err := Path()
	if err != nil {
		return Record{}, false, err
	}
	return Read(p)
}
