package endpoint_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/endpoint"
)

// TestLoadResolvesAndMakesNothing: Load reads the default config where XDG_CONFIG_HOME puts it and
// resolves its paths under the XDG directories; the directories are made only when a mode asks,
// 0700.
func TestLoadResolvesAndMakesNothing(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(d, filepath.Join(home, d))
	}
	if err := os.MkdirAll(filepath.Join(home, "XDG_CONFIG_HOME", "autodoc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "XDG_CONFIG_HOME", "autodoc", "config.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := endpoint.Load("")
	if err != nil {
		t.Fatal(err)
	}
	want := endpoint.Paths{
		Socket:   filepath.Join(home, "XDG_RUNTIME_DIR", "autodoc.sock"),
		Store:    filepath.Join(home, "XDG_DATA_HOME", "autodoc", "autodoc.db"),
		StateDir: filepath.Join(home, "XDG_STATE_HOME", "autodoc"),
	}
	p, err := e.Paths()
	if err != nil || p != want || e.ConfigPath != filepath.Join(home, "XDG_CONFIG_HOME", "autodoc", "config.toml") || e.Config == nil {
		t.Fatalf("Load: %+v, paths %+v, %v; want %+v", e, p, err, want)
	}
	if err := e.MakeStateDir(); err != nil {
		t.Fatal(err)
	}
	if err := e.MakeDataDir(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.StateDir, filepath.Dir(p.Store)} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, %v; want a 0700 directory", d, fi, err)
		}
	}
	bad := filepath.Join(home, "bad.toml")
	if err := os.WriteFile(bad, []byte("not = [toml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := endpoint.Load(bad); err == nil {
		t.Error("an invalid config loaded")
	}
}

// TestMakeDirSaysWhichFailed: a directory that cannot be made is named.
func TestMakeDirSaysWhichFailed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[server]\nstate_dir = \""+filepath.Join(file, "state")+"\"\ndata_dir = \""+filepath.Join(file, "data")+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e, err := endpoint.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.MakeStateDir(); err == nil || !strings.Contains(err.Error(), filepath.Join(file, "state")) {
		t.Fatalf("MakeStateDir under a file: %v", err)
	}
	if err := e.MakeDataDir(); err == nil || !strings.Contains(err.Error(), filepath.Join(file, "data")) {
		t.Fatalf("MakeDataDir under a file: %v", err)
	}
}

// TestCheckStore: a daemon that says it serves another store is refused, naming both; one that
// says nothing, or this store, is let through.
func TestCheckStore(t *testing.T) {
	store := filepath.Join(t.TempDir(), "autodoc.db")
	if err := endpoint.CheckStore("/s", store, ""); err != nil {
		t.Errorf("a daemon too old to say: %v", err)
	}
	var mm *endpoint.StoreMismatchError
	if err := endpoint.CheckStore("/s", store, "another"); !errors.As(err, &mm) || mm.Served != "another" || mm.Addr != "/s" || mm.StorePath != store {
		t.Fatalf("another store's daemon: %v", err)
	}
	if mm.Error() == "" {
		t.Error("the mismatch says nothing")
	}
}
