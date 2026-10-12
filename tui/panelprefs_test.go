package tui

import (
	"reflect"
	"strings"
	"testing"
)

// Observe the live sources inside the same loop turn as SetPref, before an
// asynchronous persistence answer can be applied. Rows publish their own
// effects; correctness must not depend on a full applyPrefs side effect.
func TestPanelPreferenceRowsApplyLiveAndQueueWritesInOrder(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	json := agentProfilesJSON([]agentProfile{{Name: "alpha", Command: "false"}})
	got := onLoop(r, func() bool {
		before := len(r.h.prefWrites)
		r.h.terminals.c.SetPref(prefTermEdge, "left")
		r.h.agents.c.SetPref(prefAgentEdge, "center")
		r.h.agents.c.SetPref(prefAgentProfiles, json)
		r.h.agents.c.SetPref(prefAgentDefault, "alpha")
		for name, want := range map[string]string{
			"App.terminalEdge": "left", "App.terminalSize": "40", "App.terminalEdgeIndex": "2",
			"App.agentEdge": "center", "App.agentSize": "80", "App.agentEdgeIndex": "4",
		} {
			v, ok := r.h.p.Tree().Source(name)
			if !ok || v.Raw != want {
				return false
			}
		}
		status, _ := r.h.p.Tree().Source("App.agentProfilesStatus")
		if !strings.Contains(status.Raw, "SPC g opens alpha") || r.h.agents.defaultProfile() != "alpha" || len(r.h.agents.profiles()) != 1 {
			return false
		}
		want := [][2]string{{prefTermEdge, "left"}, {prefAgentEdge, "center"}, {prefAgentProfiles, json}, {prefAgentDefault, "alpha"}}
		return reflect.DeepEqual(r.h.prefWrites[before:], want)
	})
	if !got {
		t.Fatal("panel preference rows did not update live sources or preserve persistence order")
	}
}

func TestAgentValuePreferencesDecodeWithoutSharedMutableProfileState(t *testing.T) {
	p := prefsOf(map[string]any{
		prefAgentProfiles: `[{"name":"alpha","command":"alpha"}]`, prefAgentDefault: "alpha",
		prefTermEdge: "right", prefAgentEdge: "center",
	})
	if p.Value(prefAgentDefault) != "alpha" || p.termEdge != "right" || p.agentEdge != "center" {
		t.Fatalf("existing preferences did not read through feature rows: %+v", p)
	}
	profiles := agentProfilesOf(p.Value(prefAgentProfiles))
	profiles[0].Command = "edited local copy"
	if again := agentProfilesOf(p.Value(prefAgentProfiles)); again[0].Command != "alpha" {
		t.Fatal("editing decoded profiles mutated the saved preference snapshot")
	}
	bad := prefsOf(map[string]any{prefAgentProfiles: "not JSON", prefTermEdge: "nowhere", prefAgentEdge: "nowhere"})
	if len(agentProfilesOf(bad.Value(prefAgentProfiles))) != 0 || bad.termEdge != "bottom" || bad.agentEdge != defaultAgentEdge {
		t.Fatal("invalid saved values escaped the feature defaults")
	}
}
