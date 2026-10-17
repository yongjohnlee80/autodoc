package export_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/export"
	mermaidparse "github.com/yongjohnlee80/golib/parse/mermaid"
)

// TestTutorialsBookEmbedsAndDiagrams verifies that the 18-chapter User Reference Book
// ( plus README.md ) in docs/tutorials/ resolves every embedded .mmd diagram,
// .chart.json Vega-Lite chart, and .csv table from its companion <stem>/ folder,
// parses cleanly under golib's native Mermaid parser and Vega-Lite schema rules,
// and contains zero manual ASCII box-drawing diagrams.
func TestTutorialsBookEmbedsAndDiagrams(t *testing.T) {
	tutorialsDir, err := filepath.Abs("../../docs/tutorials")
	if err != nil {
		t.Fatal(err)
	}
	mdFiles, err := filepath.Glob(filepath.Join(tutorialsDir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(mdFiles)
	if len(mdFiles) < 19 {
		t.Fatalf("found %d markdown files in %s; want README.md + 18 chapters (19 total)", len(mdFiles), tutorialsDir)
	}

	asciiBoxChars := []rune{'┌', '┐', '└', '┘', '├', '┤', '┬', '┴', '┼', '╔', '╗', '╚', '╝'}
	allowedTopKeys := map[string]bool{
		"$schema": true, "description": true, "name": true,
		"autosize": true, "padding": true, "background": true, "usermeta": true,
		"title": true, "config": true, "width": true, "height": true,
		"data": true, "mark": true, "encoding": true,
	}
	allowedChannels := map[string]bool{
		"x": true, "y": true, "color": true, "fill": true, "tooltip": true,
	}
	allowedMarks := map[string]bool{
		"bar": true, "line": true, "point": true, "circle": true, "area": true,
	}

	for _, mdPath := range mdFiles {
		base := filepath.Base(mdPath)
		stem := strings.TrimSuffix(base, ".md")
		t.Run(base, func(t *testing.T) {
			src, err := os.ReadFile(mdPath)
			if err != nil {
				t.Fatal(err)
			}
			text := string(src)

			for _, r := range asciiBoxChars {
				if strings.ContainsRune(text, r) {
					t.Errorf("%s contains ASCII box-drawing character %q; use .mmd diagrams instead", base, r)
				}
			}

			lines := export.EmbedLines(src)
			if len(lines) == 0 {
				t.Fatalf("%s has no ![caption](file) embeds", base)
			}

			var mmdCount, chartCount, csvCount int
			for _, el := range lines {
				if !el.Alone {
					t.Errorf("%s:%d: embed (%s) is not alone on its paragraph line and will degrade to italic alt text", base, el.From, el.Dest)
				}
				assetPath := filepath.Join(tutorialsDir, stem, el.Dest)
				assetBytes, err := os.ReadFile(assetPath)
				if err != nil {
					t.Fatalf("%s:%d: missing companion asset %s: %v", base, el.From, assetPath, err)
				}

				switch {
				case strings.HasSuffix(el.Dest, ".mmd"):
					mmdCount++
					if _, err := mermaidparse.Parse(string(assetBytes)); err != nil {
						t.Errorf("%s: mermaid parse error in %s: %v", base, el.Dest, err)
					}
				case strings.HasSuffix(el.Dest, ".chart.json"):
					chartCount++
					var raw map[string]json.RawMessage
					if err := json.Unmarshal(assetBytes, &raw); err != nil {
						t.Errorf("%s: invalid JSON in %s: %v", base, el.Dest, err)
						continue
					}
					for k := range raw {
						if !allowedTopKeys[k] {
							t.Errorf("%s: %s has unsupported top-level Vega-Lite key %q", base, el.Dest, k)
						}
					}
					var spec struct {
						Mark any `json:"mark"`
						Data struct {
							Values []map[string]any `json:"values"`
						} `json:"data"`
						Encoding map[string]struct {
							Field string `json:"field"`
							Type  string `json:"type"`
						} `json:"encoding"`
					}
					if err := json.Unmarshal(assetBytes, &spec); err != nil {
						t.Errorf("%s: %s failed structural unmarshal: %v", base, el.Dest, err)
						continue
					}
					var markType string
					switch m := spec.Mark.(type) {
					case string:
						markType = m
					case map[string]any:
						if mt, ok := m["type"].(string); ok {
							markType = mt
						}
					}
					if !allowedMarks[markType] {
						t.Errorf("%s: %s has unsupported mark %q", base, el.Dest, markType)
					}
					if len(spec.Data.Values) == 0 {
						t.Errorf("%s: %s has empty data.values", base, el.Dest)
					}
					for ch, enc := range spec.Encoding {
						if !allowedChannels[ch] {
							t.Errorf("%s: %s has unsupported encoding channel %q", base, el.Dest, ch)
						}
						if strings.ContainsAny(enc.Field, ".[]\\") {
							t.Errorf("%s: %s channel %q field %q contains unsupported path characters", base, el.Dest, ch, enc.Field)
						}
					}
				case strings.HasSuffix(el.Dest, ".csv"):
					csvCount++
					records, err := csv.NewReader(bytes.NewReader(assetBytes)).ReadAll()
					if err != nil {
						t.Errorf("%s: CSV parse error in %s: %v", base, el.Dest, err)
					} else if len(records) < 2 {
						t.Errorf("%s: CSV %s has %d rows; want header + at least 1 data row", base, el.Dest, len(records))
					}
				default:
					t.Errorf("%s:%d: unexpected embed extension on %q", base, el.From, el.Dest)
				}
			}

			if mmdCount == 0 || chartCount == 0 || csvCount == 0 {
				t.Errorf("%s embeds: mmd=%d chart=%d csv=%d; want at least 1 of each (.mmd, .chart.json, .csv)", base, mmdCount, chartCount, csvCount)
			}

			htmlBytes, err := export.RenderWith(src, export.HTML, "dark", export.Options{
				Document:    mdPath,
				SourceSpans: true,
			})
			if err != nil {
				t.Fatalf("%s: export.RenderWith failed: %v", base, err)
			}
			htmlStr := string(htmlBytes)
			if got := strings.Count(htmlStr, `<pre class="mermaid">`); got != mmdCount {
				t.Errorf("%s: rendered %d <pre class=\"mermaid\"> blocks; want %d", base, got, mmdCount)
			}
			if got := strings.Count(htmlStr, `<pre class="vega-lite"`); got != chartCount {
				t.Errorf("%s: rendered %d <pre class=\"vega-lite\"> blocks; want %d", base, got, chartCount)
			}
			if got := strings.Count(htmlStr, `<table>`); got < csvCount {
				t.Errorf("%s: rendered %d <table> blocks; want at least %d", base, got, csvCount)
			}
		})
	}
}
