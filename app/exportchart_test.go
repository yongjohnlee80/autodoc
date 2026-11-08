package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// Either build draws an exported chart as SVG, its labels written, in the export's palette: the GUI
// build with golib's gui/vegalite, a build without the GUI with chart/vegalite. A chart outside
// the subset stays its spec.
func TestEveryBuildDrawsAnExportedChart(t *testing.T) {
	spec := `{"mark":"bar","data":{"values":[{"k":"Apples","v":3},{"k":"Pears","v":5}]},` +
		`"encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}`
	svg, err := chartSVG([]byte(spec), "light")
	if err != nil || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, ">Apples</text>") || !strings.Contains(svg, ">Pears</text>") {
		t.Fatalf("the chart: %v\n%s", err, svg)
	}
	if !strings.Contains(svg, `fill="#2e2a24"`) { // light's text colour on the labels
		t.Errorf("the labels are not in the palette's text colour:\n%.400s", svg)
	}
	if _, err := chartSVG([]byte(`{"mark":"boxplot","data":{"values":[]}}`), "dark"); err == nil {
		t.Error("a chart outside the subset was drawn")
	}
}

// ruleByField is a chart outside golib's vegalite subset: a line, and a rule coloured by a field.
const ruleByField = `{"layer":[{"data":{"values":[{"d":"2026-10-01","v":1},{"d":"2026-10-02","v":2}]},"mark":"line",` +
	`"encoding":{"x":{"field":"d","type":"temporal"},"y":{"field":"v","type":"quantitative"}}},` +
	`{"data":{"values":[{"v":1.5,"s":"lvl"}]},"mark":"rule",` +
	`"encoding":{"y":{"field":"v","type":"quantitative"},"color":{"field":"s","type":"nominal"}}}]}`

// A chart golib cannot draw says why in its export, in golib's words: chartSVG answers vegalite's
// error, and the exported page captions the chart's spec with it, as a .chart.json embed and as a
// vega-lite fence.
func TestAnExportedChartGolibCannotDrawSaysWhy(t *testing.T) {
	if _, err := chartSVG([]byte(ruleByField), "light"); err == nil || !strings.Contains(err.Error(), "a rule coloured by a field") {
		t.Fatalf("chartSVG of a rule coloured by a field: %v; want vegalite's reason", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "levels.chart.json"), []byte(ruleByField), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "report.md")
	source := "# Report\n\n![levels](levels.chart.json)\n\n```vega-lite\n" + ruleByField + "\n```\n"
	page, err := export.RenderWith([]byte(source), export.HTML, "light", export.Options{Document: doc, Chart: chartSVG})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(page), `<p class="chart-error">chart not drawn: `); n != 2 || !strings.Contains(string(page), "a rule coloured by a field") {
		t.Fatalf("%d captions (want 2, the embed and the fence), golib's reason shown %v:\n%s",
			n, strings.Contains(string(page), "a rule coloured by a field"), page)
	}
}

// The command line's exports draw their charts: autodoc --export html, and --export site, write a
// note's vega-lite fence as SVG with its labels, in this build, the hook wired into each.
func TestTheCommandLineExportDrawsCharts(t *testing.T) {
	note := "# Fruit\n\n```vega-lite\n" + `{"mark":"bar","data":{"values":[{"k":"Apples","v":3},{"k":"Pears","v":5}]},` +
		`"encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}` + "\n```\n"
	drawn := func(what string, page []byte) {
		t.Helper()
		if s := string(page); !strings.Contains(s, "<svg") || !strings.Contains(s, ">Apples</text>") || !strings.Contains(s, ">Pears</text>") {
			t.Errorf("%s: the chart is not drawn as SVG with its labels:\n%.600s", what, s)
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "note.md")
	if err := os.WriteFile(src, []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "note.html")
	if code, msg := runMain(t, nil, "--export", "html", "--output", out, "--theme", "light", src); code != 0 {
		t.Fatalf("--export html: exit %d: %s", code, msg)
	}
	page, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	drawn("--export html", page)

	docs := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"site.toml": "[[section]]\npath = \"README.md\"\nurl = \"\"\n", "README.md": note} {
		if err := os.WriteFile(filepath.Join(docs, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	site := filepath.Join(dir, "_site")
	if code, msg := runMain(t, nil, "--export", "site", "--output", site, docs); code != 0 {
		t.Fatalf("--export site: exit %d: %s", code, msg)
	}
	index, err := os.ReadFile(filepath.Join(site, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	drawn("--export site", index)
}

// The UI is launched with the export's chart drawing, so a page the pane opens in the browser draws
// its Vega-Lite charts as --export does, in either build.
func TestTheUIDrawsChartsInTheBrowsersPage(t *testing.T) {
	o := launchOptions("", "", "", build{})
	if o.Chart == nil {
		t.Fatal("the UI's launch options carry no chart drawing: a page opened in the browser keeps its Vega-Lite spec")
	}
	spec := `{"mark":"bar","data":{"values":[{"k":"Apples","v":3}]},` +
		`"encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}`
	svg, err := o.Chart([]byte(spec), "light")
	if err != nil || !strings.Contains(svg, "<svg") || !strings.Contains(svg, "Apples") {
		t.Fatalf("the UI's chart drawing gave %.80q, %v; want the chart's SVG, its label written", svg, err)
	}
}
