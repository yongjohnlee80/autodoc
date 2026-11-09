//go:build !gui

package tui

import (
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"strings"
	"testing"
)

// TestRelationsGlobeMapSettingsOpensTheTerminals: App.openGlobeSettings, which the Relations menu
// and Preferences name in every build, opens a terminal's own GlobeMap settings, its groups listed;
// a host without a GlobeMap among its relation views says the settings are elsewhere instead.
func TestRelationsGlobeMapSettingsOpensTheTerminals(t *testing.T) {
	kb := groupsKB(t, "[[globemap.groups]]\nquery = \"path:notes/\"\ncolour = \"#edc948\"\n")
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "kb")
	r.waitListed(t, 2)
	onLoop(r, func() bool { r.h.openGlobeSettings(); return true })
	r.s.WaitFor(t, "the GlobeMap's settings, its group listed", func(s string) bool {
		return strings.Contains(s, "GlobeMap settings") && strings.Contains(s, "path:notes/")
	})
	r.keys(t, key('q'))
	r.s.WaitFor(t, "the settings closed", func(s string) bool { return !strings.Contains(s, "GlobeMap settings") })

	// a host whose relation views hold no GlobeMap
	onLoop(r, func() bool {
		var others []feature.Feature
		for _, f := range r.h.relationViewFeatures() {
			if _, globe := f.(*cellGlobeMap); !globe {
				others = append(others, f)
			}
		}
		r.h.relationViews = others
		r.h.openGlobeSettings()
		return true
	})
	r.waitNotice(t, onLoop(r, func() string { return r.h.globeSettingsElsewhereNotice() }))
	if strings.Contains(r.s.String(), "GlobeMap settings") {
		t.Fatalf("with no GlobeMap, the settings opened:\n%s", r.s)
	}
}
