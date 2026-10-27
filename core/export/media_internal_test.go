package export

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A site's media are read through the docs folder's root, whatever their path named when a page's
// embed found them: a link out of docs (a file swapped for one after it was found), a path out of
// docs and a folder are refused, and nothing is written for them. A file is written once.
func TestSiteMediaOpenThroughTheDocsRoot(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.gif"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	docs := t.TempDir()
	for _, d := range []string{"media", filepath.Join("media", "folder.gif")} {
		if err := os.MkdirAll(filepath.Join(docs, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(docs, "media", "ok.gif"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.gif"), filepath.Join(docs, "media", "link.gif")); err != nil {
		t.Fatal(err)
	}
	written := map[string]string{}
	m, err := openSiteMedia(docs, func(rel string, data []byte) error {
		written[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.files.Close()
	for _, refused := range []string{
		filepath.Join(docs, "media", "link.gif"),
		filepath.Join(outside, "secret.gif"),
		filepath.Join(docs, "media", "folder.gif"),
	} {
		if site, ok := m.publish(refused); ok {
			t.Errorf("%s was published at %s", refused, site)
		}
	}
	for range 2 {
		if site, ok := m.publish(filepath.Join(docs, "media", "ok.gif")); !ok || site != "media/ok.gif" {
			t.Errorf("ok.gif: %q %v", site, ok)
		}
	}
	if len(written) != 1 || written["media/ok.gif"] != "ok" {
		t.Errorf("written %v; want ok.gif alone", written)
	}
}

// A write that fails ends the publishing: that file is not published, and no later one is written.
func TestSiteMediaStopAtAFailedWrite(t *testing.T) {
	docs := t.TempDir()
	for _, name := range []string{"a.gif", "b.gif"} {
		if err := os.WriteFile(filepath.Join(docs, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	full := errors.New("disk full")
	var calls []string
	m, err := openSiteMedia(docs, func(rel string, _ []byte) error {
		calls = append(calls, rel)
		return full
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.files.Close()
	if _, ok := m.publish(filepath.Join(docs, "a.gif")); ok || !errors.Is(m.err, full) {
		t.Fatalf("a failed write: published %v, err %v", ok, m.err)
	}
	if _, ok := m.publish(filepath.Join(docs, "b.gif")); ok || len(calls) != 1 {
		t.Errorf("after a failed write: published %v, writes %v", ok, calls)
	}
}
