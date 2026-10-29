package localfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/fsbounds"
)

// TestAWriteUnderBoundsWritesNothing: localfs is a route to the disk outside every workspace's
// guarded filesystem, so under a gated script's bounds it refuses, and the file is not made; the
// user's own write is made.
func TestAWriteUnderBoundsWritesNothing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "out.txt")
	ctx := fsbounds.With(context.Background(), fsbounds.Bounds{Within: "kb"})
	if err := WriteFile(ctx, p, []byte("x")); !errors.Is(err, fsbounds.ErrRefused) {
		t.Errorf("a bounded write: %v, want refused", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("the refused write made the file: %v", err)
	}
	if err := WriteFile(context.Background(), p, []byte("x")); err != nil {
		t.Errorf("the user's write: %v", err)
	}
}
