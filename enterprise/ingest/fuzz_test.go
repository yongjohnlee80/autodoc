package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad: no .view file panics Load: a view it cannot use is a *ViewError, and nothing else.
func FuzzLoad(f *testing.F) {
	f.Add(trackView)
	f.Add("---\nversion: 1\nname: x\nsource: sqlite\n---\n")
	f.Add("---\nversion: 1\nname: track\nsource: postgres\nargs: [after_ts, after_id, upto_ts]\nprocess: SELECT 1\nexport: \"{{.id\"\n---\n")
	f.Add("")
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, src string) {
		path := filepath.Join(dir, "v.view")
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, nil); err != nil {
			if _, ok := err.(*ViewError); !ok {
				t.Fatalf("Load answered %T, not a *ViewError: %v", err, err)
			}
		}
	})
}
