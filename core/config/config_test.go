package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
)

func load(t *testing.T, body string) (*config.Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(p)
}

func TestLoadFillsDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c, err := load(t, "[[workspace]]\nname = \"kb\"\nroot = \"~/notes\"\n")
	if err != nil {
		t.Fatal(err)
	}
	w := c.Workspaces[0]
	if w.Root != filepath.Join(home, "notes") {
		t.Errorf("root = %q, want ~ expanded", w.Root)
	}
	if !reflect.DeepEqual(w.Include, config.DefaultInclude) || !reflect.DeepEqual(w.Exclude, config.DefaultExclude) {
		t.Errorf("patterns = %v / %v, want the defaults", w.Include, w.Exclude)
	}
	if c.Follow.PollInterval.Duration != config.DefaultPollInterval {
		t.Errorf("poll interval = %v", c.Follow.PollInterval.Duration)
	}
}

func TestLoadReadsEverySetting(t *testing.T) {
	c, err := load(t, `
[server]
socket = "/tmp/a.sock"
state_dir = "/tmp/autodoc-state"
[follow]
poll_interval = "500ms"
[[workspace]]
name = "kb"
root = "/srv/kb"
include = ["docs/**/*.md", "*.markdown"]
exclude = []
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Socket != "/tmp/a.sock" || c.Server.StateDir != "/tmp/autodoc-state" || c.Follow.PollInterval.Duration != 500*time.Millisecond {
		t.Errorf("server/follow = %+v %+v", c.Server, c.Follow)
	}
	if w := c.Workspaces[0]; len(w.Include) != 2 || len(w.Exclude) != 0 {
		t.Errorf("an explicit empty exclude must stay empty: %+v", w)
	}
}

func TestLoadRejects(t *testing.T) {
	for _, c := range []struct{ name, body, want string }{
		{"unknown key", "[[workspace]]\nname = \"kb\"\nroot = \"/x\"\nroots = \"/y\"\n", "unknown settings"},
		{"duplicate name", "[[workspace]]\nname = \"a\"\nroot = \"/x\"\n[[workspace]]\nname = \"a\"\nroot = \"/y\"\n", "twice"},
		{"relative root", "[[workspace]]\nname = \"a\"\nroot = \"notes\"\n", "absolute"},
		{"no name", "[[workspace]]\nroot = \"/x\"\n", "name is required"},
		{"a name with a separator", "[[workspace]]\nname = \"a/b\"\nroot = \"/x\"\n", "path separator"},
		{"bad pattern", "[[workspace]]\nname = \"a\"\nroot = \"/x\"\ninclude = [\"[\"]\n", "pattern"},
		{"absolute pattern", "[[workspace]]\nname = \"a\"\nroot = \"/x\"\nexclude = [\"/etc/**\"]\n", "pattern"},
		{"bad duration", "[follow]\npoll_interval = \"soon\"\n", "invalid"},
		{"not TOML", "[[workspace\n", "invalid"},
	} {
		_, err := load(t, c.body)
		if !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrInvalid mentioning %q", c.name, err, c.want)
		}
	}
}

func TestPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	if p, _ := config.DefaultPath(); p != "/cfg/autodoc/config.toml" {
		t.Errorf("DefaultPath = %q", p)
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if d, err := (config.Server{}).StateDirPath(); err != nil || d != filepath.Join(state, "autodoc") {
		t.Errorf("StateDirPath = %q, %v", d, err)
	} else if fi, _ := os.Stat(d); fi.Mode().Perm() != 0o700 {
		t.Errorf("the state directory is %v, want 0700", fi.Mode().Perm())
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if s, _ := (config.Server{}).SocketPath(); s != "/run/user/1000/autodoc.sock" {
		t.Errorf("SocketPath = %q", s)
	}
	if _, err := (config.Server{Socket: "/" + strings.Repeat("x", 120)}).SocketPath(); !errors.Is(err, config.ErrInvalid) {
		t.Errorf("an over-long socket path: %v, want ErrInvalid before bind", err)
	}
}
