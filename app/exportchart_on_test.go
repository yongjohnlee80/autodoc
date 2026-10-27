//go:build gui

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// In the GUI build an exported chart is drawn as SVG by golib's gui/vegalite, its labels written,
// in the export's palette; a chart outside the subset stays its spec.
func TestTheGUIBuildDrawsAnExportedChart(t *testing.T) {
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
