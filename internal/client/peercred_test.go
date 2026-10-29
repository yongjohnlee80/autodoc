package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/peercred"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// TestASessionRefusesAnotherUsersDaemon: a session whose socket another user's process listens on
// (played: peercred.PlayUser) is refused with ErrOtherUser, at once rather than after the probe
// window, and spawns nothing; the control, the same daemon as this user's, is connected to.
func TestASessionRefusesAnotherUsersDaemon(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no peer credentials on " + runtime.GOOS)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sock := filepath.Join(shortDir(t), "s.sock")
	daemonSaying(t, sock, "")

	restore := peercred.PlayUser(os.Getuid() + 1)
	defer restore()
	spawned := false
	s := NewSession(sock, func() (string, error) { spawned = true; return "", nil }).As(rpc.CLIName)
	began := time.Now()
	if err := s.Connect(ctx); !errors.Is(err, peercred.ErrOtherUser) {
		s.Close()
		t.Fatalf("another user's daemon: %v, want ErrOtherUser", err)
	}
	if took := time.Since(began); took > time.Second || spawned {
		t.Errorf("the refusal took %s, spawned %v; want it at once, and no spawn", took, spawned)
	}

	restore()
	if err := s.Connect(ctx); err != nil {
		t.Fatalf("this user's daemon: %v", err)
	}
	s.Close()
}
