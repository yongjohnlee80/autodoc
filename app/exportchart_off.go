//go:build !gui

package app

// chartSVG is the GUI build's (exportchart_on.go): without it, an exported chart stays its spec.
var chartSVG func(spec []byte, theme string) (string, bool)
