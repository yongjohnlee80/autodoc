//go:build gui

package app

import (
	"github.com/yongjohnlee80/golib/gui/mermaid"
	"github.com/yongjohnlee80/golib/gui/vegalite"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

func init() {
	// golib's own tables, the ones its parser and reader dispatch from
	diagramSubset = func() drawnSubset {
		var kinds []drawnKind
		drawn := map[pm.Kind]bool{}
		for _, k := range mermaid.Kinds() {
			kinds = append(kinds, drawnKind{name: k.Kind.String(), keywords: k.Keywords})
			drawn[k.Kind] = true
		}
		var undrawn []string
		for _, k := range pm.Kinds() {
			if !drawn[k.Kind] {
				undrawn = append(undrawn, k.Kind.String())
			}
		}
		v := vegalite.Subset()
		return drawnSubset{kinds: kinds, undrawn: undrawn, marks: v.Marks, channels: v.Channels, fieldTypes: v.FieldTypes,
			scaleTypes: v.ScaleTypes, aggs: v.Aggregates, layers: v.Layers, maxRows: v.MaxRows,
			maxBands: v.MaxBands, maxSeries: v.MaxSeries, passed: v.Passed}
	}
}
