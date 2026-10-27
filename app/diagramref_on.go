//go:build gui

package app

import (
	"github.com/yongjohnlee80/golib/gui/mermaid"
	"github.com/yongjohnlee80/golib/gui/vegalite"
)

func init() {
	// golib's own tables, the ones its parser and reader dispatch from
	diagramSubset = func() drawnSubset {
		var kinds []drawnKind
		for _, k := range mermaid.Kinds() {
			kinds = append(kinds, drawnKind{name: k.Kind.String(), keywords: k.Keywords})
		}
		v := vegalite.Subset()
		return drawnSubset{kinds: kinds, marks: v.Marks, channels: v.Channels, fieldTypes: v.FieldTypes,
			scaleTypes: v.ScaleTypes, aggs: v.Aggregates, layers: v.Layers, maxRows: v.MaxRows,
			maxBands: v.MaxBands, maxSeries: v.MaxSeries, passed: v.Passed}
	}
}
