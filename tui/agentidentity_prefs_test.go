package tui

// The agents' identity cells that read the host's preferences as they are stored (prefsFor):
// a legacy default migrated to its codename.

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

func TestProfileRenamesKeepOwnAndUntouchedLegacyCodenames(t *testing.T) {
	c := newIdentityHostCopy(`[{"name":"Claude Opus","command":"one"},{"name":"Other Agent","command":"two"}]`)
	c.p = prefsFor(map[string]any{prefAgentProfiles: c.p.Value(prefAgentProfiles), prefAgentDefaultCopy: "Other Agent"}, false)
	a := testAgentsCopy(c, "", "")
	a.startEditAgent(1)
	a.saveAgent("Renamed", "two")
	profiles := a.profiles()
	if profiles[0].Codename != "claude-opus" || profiles[1].Codename != "other-agent" || a.defaultProfile() != "other-agent" {
		t.Fatalf("display rename changed an identity or lost the name-based default: %+v, default %q", profiles, a.defaultProfile())
	}
	if raw := c.p.Value(prefAgentProfiles); !strings.Contains(raw, `"codename":"other-agent"`) {
		t.Fatalf("edit did not persist its derived codename: %s", raw)
	}
}

func TestLegacyDefaultNameMigratesWithoutConfusingAnotherCodename(t *testing.T) {
	c := newIdentityHostCopy(`[{"name":"Foo","command":"one"},{"name":"foo","command":"two"},{"name":"foo-2","command":"three"}]`)
	c.p = prefsFor(map[string]any{prefAgentProfiles: c.p.Value(prefAgentProfiles), prefAgentDefaultCopy: "foo-2"}, false)
	a := testAgentsCopy(c, "", "")
	if p, ok := a.profile(a.defaultProfile()); !ok || p.Command != "three" {
		t.Fatalf("legacy name was mistaken for another allocated codename: %+v", p)
	}
	a.saveAgentProfile("Other", "other", "other")
	if a.defaultProfile() != "foo-2-2" || c.p.Value(prefAgentDefaultCodename) != "foo-2-2" {
		t.Fatal("list save did not persist the intended default identity")
	}
	a.makeAgentDefault(1)
	if p, ok := a.profile(a.defaultProfile()); !ok || p.Command != "two" {
		t.Fatal("canonical default still resolved through display labels")
	}
	a.removeAgentConfirmed(a.profiles()[1])
	if a.defaultProfile() != "" || c.p.Value(prefAgentDefaultCodename) != "" {
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
				prefAgentProfiles:    `[{"name":"A","command":"one"},{"name":"B","command":"two"}]`,
				prefAgentDefaultCopy: "A",
			}
			if tc.present {
				raw[prefAgentDefaultCodename] = tc.canonical
			}
			values, migrated := migrateAgentDefault(raw)
			if migrated == tc.present {
				t.Fatal("migration ignored canonical key presence")
			}
			var rows prefs
			feature.ReadRows(&rows, values, agentPrefRows(nil))
			for _, p := range []prefs{rows, prefsFor(raw, false), prefsFor(raw, true)} {
				c := newIdentityHostCopy(`[]`)
				c.p = p
				if got := testAgentsCopy(c, "", "").defaultProfile(); got != tc.want {
					t.Fatalf("restart default %q, want %q", got, tc.want)
				}
			}
			if _, changed := migrateAgentDefault(values); changed {
				t.Fatal("canonical preferences migrated more than once")
			}
			if _, added := raw[prefAgentDefaultCodename]; added != tc.present {
				t.Fatal("migration mutated the raw store map")
			}
		})
	}
}
