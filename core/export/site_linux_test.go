//go:build linux

package export_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// A site publishes the bytes of a file in docs, never of one outside it, while the file a page draws
// is swapped back and forth with a link out of docs (renameat2's exchange, so the name always
// names one of the two). The swap lands between the page's embed finding the file and the site
// reading it often enough that a reader which checks the path and then opens it again publishes the
// outside file within a few hundred builds; the cell also needs the file itself published, so a
// build that refuses everything does not pass it.
func TestASitesMediaStayInDocsWhileAFileIsSwapped(t *testing.T) {
	secret := []byte("GIF89a-outside")
	outside := fixture(t, map[string]string{"secret.gif": string(secret)})
	docs := fixture(t, map[string]string{
		"site.toml":           mediaMap,
		"README.md":           "# Front\n\n![asset](media/asset.gif)\n",
		"media/asset.gif":     "GIF89a-public",
		"tutorials/README.md": "# T\n",
	})
	file, swap := filepath.Join(docs, "media", "asset.gif"), filepath.Join(docs, "media", "swap.gif")
	if err := os.Symlink(filepath.Join(outside, "secret.gif"), swap); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := unix.Renameat2(unix.AT_FDCWD, file, unix.AT_FDCWD, swap, unix.RENAME_EXCHANGE); err != nil {
				t.Errorf("renameat2: %v", err)
				return
			}
		}
	})
	defer func() { close(stop); wg.Wait() }()
	published := 0
	for i := range 2000 {
		var leaked bool
		err := export.Site(docs, export.SiteOptions{}, func(rel string, data []byte) error {
			if rel == "media/asset.gif" {
				published++
				leaked = leaked || bytes.Equal(data, secret)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if leaked {
			t.Fatalf("build %d published the file outside docs as media/asset.gif", i)
		}
	}
	if published == 0 {
		t.Fatal("media/asset.gif was never published: the cell saw no build read it")
	}
}
