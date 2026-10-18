package client

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
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
