//go:build gui

package app

import (
	"strings"
	"testing"
)

// In the GUI build an exported chart is drawn as SVG by golib's gui/vegalite, its labels written,
// in the export's palette; a chart outside the subset stays its spec.
func TestTheGUIBuildDrawsAnExportedChart(t *testing.T) {
	spec := `{"mark":"bar","data":{"values":[{"k":"Apples","v":3},{"k":"Pears","v":5}]},` +
		`"encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}`
	svg, ok := chartSVG([]byte(spec), "light")
	if !ok || !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, ">Apples</text>") || !strings.Contains(svg, ">Pears</text>") {
		t.Fatalf("the chart: ok %v\n%s", ok, svg)
	}
	if !strings.Contains(svg, `fill="#2e2a24"`) { // light's text colour on the labels
		t.Errorf("the labels are not in the palette's text colour:\n%.400s", svg)
	}
	if _, ok := chartSVG([]byte(`{"mark":"boxplot","data":{"values":[]}}`), "dark"); ok {
		t.Error("a chart outside the subset was drawn")
	}
}
