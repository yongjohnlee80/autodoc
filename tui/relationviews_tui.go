//go:build !gui

package tui

import tuidecl "github.com/yongjohnlee80/golib/tui/decl"

// A terminal draws no GlobeMap and no Mindmap: Relations › Links is its view of a document's
// relations (relationmap.go).

const relationMenuQML, relationViewsQML = "", ""

func (h *Host) relationViewFeatures() []Feature { return nil }

// openGlobeSettings is App.openGlobeSettings, which Preferences.qml names in every build.
func (h *Host) openGlobeSettings()                { h.notify(globeSettingsElsewhere) }
func (h *Host) relationViewTypes() []tuidecl.Type { return nil }
