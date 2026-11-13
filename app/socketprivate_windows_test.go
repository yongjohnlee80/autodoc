package app

import (
	"context"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// socketIsPrivate reports whether the daemon's endpoint sock is this user's alone, as Windows keeps
// a named pipe: its DACL, which ipc.Dial refuses unless it admits this user alone.
func socketIsPrivate(t *testing.T, sock string) error {
	t.Helper()
	c, err := ipc.Dial(context.Background(), sock)
	if err != nil {
		return err
	}
	return c.Close()
}
