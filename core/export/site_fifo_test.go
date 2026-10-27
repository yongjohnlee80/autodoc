//go:build linux || darwin

package export_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// fifoDocs names, in a child's environment, the docs folder its cell builds (inChild).
const fifoDocs = "AUTODOC_TEST_FIFO_DOCS"

// inChild runs the named cell again in a child of this test binary, with docs as fifoDocs, and fails
// when it fails or has not finished within 10 s. An open that waits on a FIFO has nothing to wait on
// but a writer, so the cell that may block runs where a deadline can end it: the package goes on.
func inChild(t *testing.T, name, docs string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+name+"$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), fifoDocs+"="+docs)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the build waited on a FIFO instead of refusing it:\n%s", out)
	}
	if err != nil || !strings.Contains(string(out), "--- PASS: "+name) {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

// A page's embed of a FIFO is its alt text: the build neither publishes it nor waits on it for a
// writer, and the page's regular image beside it is published.
func TestASiteRefusesAFIFOEmbed(t *testing.T) {
	if docs := os.Getenv(fifoDocs); docs != "" {
		var front string
		published := map[string]bool{}
		err := export.Site(docs, export.SiteOptions{}, func(rel string, data []byte) error {
			published[rel] = true
			if rel == "index.html" {
				front = string(data)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if published["media/pipe.gif"] || !published["media/safe.gif"] || !strings.Contains(front, "<em>pipe</em>") {
			t.Fatalf("want safe.gif published and the FIFO as its alt text: published %v\n%s", published, front)
		}
		return
	}
	docs := fixture(t, map[string]string{
		"site.toml":           mediaMap,
		"README.md":           "# Front\n\n![pipe](media/pipe.gif)\n\n![safe](media/safe.gif)\n",
		"media/safe.gif":      "GIF89a-safe",
		"tutorials/README.md": "# T\n",
	})
	if err := unix.Mkfifo(filepath.Join(docs, "media", "pipe.gif"), 0o600); err != nil {
		t.Fatal(err)
	}
	inChild(t, "TestASiteRefusesAFIFOEmbed", docs)
}
