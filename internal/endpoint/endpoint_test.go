package endpoint_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
	"github.com/yongjohnlee80/autodoc/rpc"
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

// TestCheckStore: a daemon that says it serves another store is refused, naming both; one too old
// to say (protocol 12) is let through; one whose protocol says its store_id and that says none is
// refused, so a hello that leaves it out cannot skip the check.
func TestCheckStore(t *testing.T) {
	store := filepath.Join(t.TempDir(), "autodoc.db")
	if err := endpoint.CheckStore("/s", store, "", 12); err != nil {
		t.Errorf("a daemon too old to say: %v", err)
	}
	for _, protocol := range []int64{13, rpc.Protocol} {
		if err := endpoint.CheckStore("/s", store, "", protocol); !errors.Is(err, endpoint.ErrNoStoreID) {
			t.Errorf("a protocol %d daemon that says no store: %v, want ErrNoStoreID", protocol, err)
		}
	}
	var mm *endpoint.StoreMismatchError
	if err := endpoint.CheckStore("/s", store, "another", rpc.Protocol); !errors.As(err, &mm) || mm.Served != "another" || mm.Addr != "/s" || mm.StorePath != store {
		t.Fatalf("another store's daemon: %v", err)
	}
	if mm.Error() == "" {
		t.Error("the mismatch says nothing")
	}
}

// TestTheStoresTempFilesGoBesideIt: SQLite's temporary files (a VACUUM's copy) are placed by
// SQLITE_TMPDIR, set to the store's directory when the user set none, before any connection opens
// (the driver takes the environment at the process's first): nothing is changed under a running
// connection. A value the user set is kept.
func TestTheStoresTempFilesGoBesideIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autodoc.db")
	t.Setenv("SQLITE_TMPDIR", "")
	endpoint.TempBeside(path)
	if got := os.Getenv("SQLITE_TMPDIR"); got != filepath.Dir(path) {
		t.Errorf("SQLITE_TMPDIR = %q, want the store's directory %q", got, filepath.Dir(path))
	}
	t.Setenv("SQLITE_TMPDIR", "/the/users/choice")
	endpoint.TempBeside(path)
	if got := os.Getenv("SQLITE_TMPDIR"); got != "/the/users/choice" {
		t.Errorf("a SQLITE_TMPDIR the user set became %q", got)
	}
}

// TestAPathThatDoesNotResolveIsSaid: with no home directory and no XDG directory, a path left to
// its default does not resolve, and a mode asking for it (Paths, MakeStateDir, MakeDataDir) says
// so instead of making anything; a socket path over the limit is refused the same way.
func TestAPathThatDoesNotResolveIsSaid(t *testing.T) {
	for _, d := range []string{"HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(d, "")
	}
	dir := t.TempDir()
	unresolved := &endpoint.Endpoints{Config: &config.Config{}}
	if _, err := unresolved.Paths(); err == nil {
		t.Error("Paths resolved a state directory with no home")
	}
	if err := unresolved.MakeStateDir(); err == nil {
		t.Error("MakeStateDir made a state directory with no home")
	}
	if err := unresolved.MakeDataDir(); err == nil {
		t.Error("MakeDataDir made a data directory with no home")
	}
	for name, s := range map[string]config.Server{
		"a socket over the limit": {StateDir: dir, Socket: filepath.Join(dir, strings.Repeat("s", 120)+".sock")},
		"a store with no home":    {StateDir: dir, Socket: filepath.Join(dir, "a.sock")},
	} {
		e := &endpoint.Endpoints{Config: &config.Config{Server: s}}
		if _, err := e.Paths(); err == nil {
			t.Errorf("%s: Paths resolved", name)
		}
	}
}
