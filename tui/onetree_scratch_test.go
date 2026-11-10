package tui

import (
	"testing"

	"github.com/yongjohnlee80/golib/vfs/local"

	"github.com/yongjohnlee80/autodoc/core/docs"
)

// onetree is the mutant's daemon disk: Unix's one tree at "/" (scratch, not for merge).
func onetree(t *testing.T, opts ...docs.Option) *docs.Docs {
	disk, err := local.New("/")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	return docs.New(disk, nil, opts...)
}
