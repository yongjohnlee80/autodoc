package client

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/userperm"
	"github.com/yongjohnlee80/autodoc/internal/userperm/userpermtest"
)

// TestMain is a spawned daemon's stand-in when SpawnServe runs this test binary with --serve: it
// says what profile switch it inherited, into the serve log SpawnServe gave it, and exits.
func TestMain(m *testing.M) {
	if os.Getenv("AUTODOC_SPAWN_TEST_CHILD") == "1" && slices.Contains(os.Args[1:], "--serve") {
		fmt.Printf("profile=%q\n", os.Getenv(ProfileEnv))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A daemon the UI spawns does not inherit the UI's profile switch: the monitor's directory is the
// UI's, and a daemon contending for it would not start. Everything else is inherited.
func TestASpawnedDaemonDoesNotInheritTheProfileSwitch(t *testing.T) {
	t.Setenv("AUTODOC_SPAWN_TEST_CHILD", "1")
	t.Setenv(ProfileEnv, "/tmp/the-uis-monitor")
	log, err := SpawnServe("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var got string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(log); strings.Contains(string(b), "profile=") {
			got = strings.TrimSpace(string(b))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != `profile=""` {
		t.Fatalf("the spawned daemon said %q; want it to have no %s", got, ProfileEnv)
	}
	if env := serveEnv([]string{"A=1", ProfileEnv + "=/x", ProfileEnv + "_OTHER=y", "B=2"}); !slices.Equal(env, []string{"A=1", ProfileEnv + "_OTHER=y", "B=2"}) {
		t.Errorf("serveEnv kept or dropped the wrong names: %v", env)
	}
}

// TestTheDaemonsLogIsPrivateAndReadWhileWritten: the detached daemon's log is made private to this
// user when new, as the OS keeps a file private (internal/userperm), and another reader opens it
// while it is being written, as one reads a log; one that is there is appended to, kept.
func TestTheDaemonsLogIsPrivateAndReadWhileWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.log")
	f, err := openServeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("one\n"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "one\n" { // a second reader, the log open
		t.Fatalf("the log read while written: %q, %v", b, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if p, err := userperm.OpenPrivate(path); err != nil {
		t.Fatalf("the log is not private to this user: %v", err)
	} else {
		_ = p.Close()
	}
	again, err := openServeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.WriteString("two\n"); err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
	if b, _ := os.ReadFile(path); string(b) != "one\ntwo\n" {
		t.Fatalf("the log reopened: %q, want it appended to", b)
	}
}

// TestAnEarlierLogThatIsNotPrivateIsMadePrivate: a daemon's log an earlier version left readable by
// others (made 0600, which on Windows took its folder's access list) is made private as it is opened,
// its lines kept, and appended to.
func TestAnEarlierLogThatIsNotPrivateIsMadePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.log")
	if err := os.WriteFile(path, []byte("earlier\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userpermtest.Expose(t, path) // readable by others, as this OS says it
	f, err := openServeLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("now\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if p, err := userperm.OpenPrivate(path); err != nil {
		t.Fatalf("the earlier log is still not private: %v", err)
	} else {
		_ = p.Close()
	}
	if b, _ := os.ReadFile(path); string(b) != "earlier\nnow\n" {
		t.Fatalf("the earlier log: %q, want its lines kept and appended to", b)
	}
}
