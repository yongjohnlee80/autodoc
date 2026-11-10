package crew

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/paths"
)

// TestThePluginURLNamesTheFile: the plugin's URL is a local file: URL, absolute and escaped, so it
// names the very file wherever it is: a folder holding '#', '%', '?', a space or non-ASCII, and a
// relative path (made absolute against the working directory) all read back as their own path. On
// Windows its path starts at the drive (file:///C:/…), never the drive as a host (file://C:/…).
func TestThePluginURLNamesTheFile(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"team#1", "50%41", "a?b=c", "with space", "ünïcödé", "%", "plain"} {
		p := filepath.Join(base, name, OpenCodePluginName)
		u, err := pluginURL(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(u, "file:///") {
			t.Errorf("%q: %s is not a local file URL", name, u)
		}
		if back, err := paths.FromURL(u); err != nil || string(back) != p {
			t.Errorf("%q: %s reads back as %q %v, want the path %s", name, u, back, err, p)
		}
	}
	t.Chdir(base)
	want := filepath.Join(base, "rel", OpenCodePluginName)
	u, err := pluginURL(filepath.Join("rel", OpenCodePluginName))
	if err != nil {
		t.Fatal(err)
	}
	if back, err := paths.FromURL(u); err != nil || string(back) != want {
		t.Errorf("a relative path's URL: %s reads back as %q %v, want it absolute, %s", u, back, err, want)
	}
}
