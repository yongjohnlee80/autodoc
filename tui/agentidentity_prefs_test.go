package tui

// The agents' identity cells that read the host's preferences as they are stored (prefsFor):
// a legacy default migrated to its codename.

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

func TestProfileRenamesKeepOwnAndUntouchedLegacyCodenames(t *testing.T) {
	c := newIdentityHost(`[{"name":"Claude Opus","command":"one"},{"name":"Other Agent","command":"two"}]`)
	c.p = prefsFor(map[string]any{PrefAgentProfiles: c.p.Value(agents.PrefAgentProfiles), prefAgentDefault: "Other Agent"}, false)
	a := testAgents(c, "", "")
	a.StartEditAgent(1)
	a.SaveAgent("Renamed", "two")
	profiles := a.Profiles()
	if profiles[0].Codename != "claude-opus" || profiles[1].Codename != "other-agent" || a.DefaultProfile() != "other-agent" {
		t.Fatalf("display rename changed an identity or lost the name-based default: %+v, default %q", profiles, a.DefaultProfile())
	}
	if raw := c.p.Value(agents.PrefAgentProfiles); !strings.Contains(raw, `"codename":"other-agent"`) {
		t.Fatalf("edit did not persist its derived codename: %s", raw)
	}
}

func TestLegacyDefaultNameMigratesWithoutConfusingAnotherCodename(t *testing.T) {
	c := newIdentityHost(`[{"name":"Foo","command":"one"},{"name":"foo","command":"two"},{"name":"foo-2","command":"three"}]`)
	c.p = prefsFor(map[string]any{PrefAgentProfiles: c.p.Value(agents.PrefAgentProfiles), prefAgentDefault: "foo-2"}, false)
	a := testAgents(c, "", "")
	if p, ok := a.Profile(a.DefaultProfile()); !ok || p.Command != "three" {
		t.Fatalf("legacy name was mistaken for another allocated codename: %+v", p)
	}
	a.SaveAgentProfile("Other", "other", "other")
	if a.DefaultProfile() != "foo-2-2" || c.p.Value(agents.PrefAgentDefaultCodename) != "foo-2-2" {
		t.Fatal("list save did not persist the intended default identity")
	}
	a.MakeAgentDefault(1)
	if p, ok := a.Profile(a.DefaultProfile()); !ok || p.Command != "two" {
		t.Fatal("canonical default still resolved through display labels")
	}
	a.RemoveAgentConfirmed(a.Profiles()[1])
	if a.DefaultProfile() != "" || c.p.Value(agents.PrefAgentDefaultCodename) != "" {
		t.Fatal("removing the default resurrected the legacy label")
	}
}

func TestAgentDefaultMigrationDistinguishesMissingAndExplicitEmptyAtRestart(t *testing.T) {
	for _, tc := range []struct {
		name            string
		present         bool
		canonical, want string
	}{
		{"legacy absent", false, "", "a"},
		{"explicit clear", true, "", ""},
		{"canonical selection", true, "b", "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{
				PrefAgentProfiles:    `[{"name":"A","command":"one"},{"name":"B","command":"two"}]`,
				prefAgentDefault: "A",
			}
			if tc.present {
				raw[agents.PrefAgentDefaultCodename] = tc.canonical
			}
			values, migrated := agents.MigrateAgentDefault(raw)
			if migrated == tc.present {
				t.Fatal("migration ignored canonical key presence")
			}
			var rows prefs
			feature.ReadRows(&rows, values, agents.AgentPrefRows(nil))
			for _, p := range []prefs{rows, prefsFor(raw, false), prefsFor(raw, true)} {
				c := newIdentityHost(`[]`)
				c.p = p
				if got := testAgents(c, "", "").DefaultProfile(); got != tc.want {
					t.Fatalf("restart default %q, want %q", got, tc.want)
				}
			}
			if _, changed := agents.MigrateAgentDefault(values); changed {
				t.Fatal("canonical preferences migrated more than once")
			}
			if _, added := raw[agents.PrefAgentDefaultCodename]; added != tc.present {
				t.Fatal("migration mutated the raw store map")
			}
		})
	}
}
