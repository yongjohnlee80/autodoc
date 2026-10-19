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
	"github.com/yongjohnlee80/autodoc/core/todo"
	mermaidparse "github.com/yongjohnlee80/golib/parse/mermaid"
)

// TestReferenceBookEmbedsAndDiagrams verifies that the 18-chapter Reference Book
// ( plus README.md ) in docs/reference/ resolves every embedded .mmd diagram,
// .chart.json Vega-Lite chart, and .csv table from its companion <stem>/ folder,
// parses cleanly under golib's native Mermaid parser and Vega-Lite schema rules,
// and contains zero manual ASCII box-drawing diagrams.
func TestReferenceBookEmbedsAndDiagrams(t *testing.T) {
	tutorialsDir, err := filepath.Abs("../../docs/reference")
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
	// Place README.md first in reading order, followed by 01..18.
	ordered := make([]string, 0, len(mdFiles))
	for _, p := range mdFiles {
		if filepath.Base(p) == "README.md" {
			ordered = append(ordered, p)
		}
	}
	for _, p := range mdFiles {
		if filepath.Base(p) != "README.md" {
			ordered = append(ordered, p)
		}
	}

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

	verbs := registeredVerbs(t)

	for idx, mdPath := range ordered {
		base := filepath.Base(mdPath)
		stem := strings.TrimSuffix(base, ".md")
		t.Run(base, func(t *testing.T) {
			src, err := os.ReadFile(mdPath)
			if err != nil {
				t.Fatal(err)
			}
			text := string(src)

			checkBookPage(t, base, text, verbs)

			// Verify bottom Previous / Next navigation links.
			trimmed := strings.TrimSpace(text)
			lastLines := trimmed
			if nl := strings.LastIndex(trimmed, "\n"); nl >= 0 {
				lastLines = trimmed[nl+1:]
			}
			if idx > 0 {
				prevBase := filepath.Base(ordered[idx-1])
				wantPrev := "(./" + prevBase + ")"
				if !strings.Contains(lastLines, "Previous:") || !strings.Contains(lastLines, wantPrev) {
					t.Errorf("%s: bottom line %q missing Previous link %s", base, lastLines, wantPrev)
				}
			}
			if idx < len(ordered)-1 {
				nextBase := filepath.Base(ordered[idx+1])
				wantNext := "(./" + nextBase + ")"
				if !strings.Contains(lastLines, "Next") || !strings.Contains(lastLines, wantNext) {
					t.Errorf("%s: bottom line %q missing Next link %s", base, lastLines, wantNext)
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

// The rules every page of both books keeps (docs/reference and docs/tutorials).

// asciiBoxChars are box drawing's characters: a book draws its diagrams as Mermaid, never in text.
var asciiBoxChars = []rune{'┌', '┐', '└', '┘', '├', '┤', '┬', '┴', '┼', '╔', '╗', '╚', '╝'}

// registeredVerbs are the daemon's verbs (rpc/testdata/contracts.json) and the todo manager's.
func registeredVerbs(t *testing.T) map[string]bool {
	t.Helper()
	contractsBytes, err := os.ReadFile("../../rpc/testdata/contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Verbs []struct {
			Verb string `json:"verb"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal(contractsBytes, &catalog); err != nil {
		t.Fatal(err)
	}
	verbs := make(map[string]bool, len(catalog.Verbs)+32)
	for _, v := range catalog.Verbs {
		verbs[v.Verb] = true
	}
	for _, v := range (*todo.Manager)(nil).Verbs() {
		verbs[v.Verb] = true
	}
	return verbs
}

// checkBookPage is what every page keeps: no box drawing, and each concrete `autodoc --call <verb>`
// a registered verb.
func checkBookPage(t *testing.T, base, text string, verbs map[string]bool) {
	t.Helper()
	for _, r := range asciiBoxChars {
		if strings.ContainsRune(text, r) {
			t.Errorf("%s contains ASCII box-drawing character %q; draw it as a Mermaid diagram instead", base, r)
		}
	}
	for lineNo, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(s, "autodoc --call "); ok {
			fields := strings.Fields(after)
			if len(fields) > 0 && !strings.HasPrefix(fields[0], "<") && !verbs[fields[0]] {
				t.Errorf("%s:%d: autodoc --call %q is not a registered verb in rpc/testdata/contracts.json", base, lineNo+1, fields[0])
			}
		}
	}
}
