package tui

import (
	"strings"
	"testing"
)

// A row reads a value it admits over its default, and its default for anything else; a feature's
// own row keeps its value apart in each copy of the preferences.
func TestPrefRowsReadOverTheirDefaults(t *testing.T) {
	var p prefs
	rows := []PrefSpec{
		boolPref("t.bool", true, func(p *prefs) *bool { return &p.wrap }),
		choicePref("t.choice", "left", edges, func(p *prefs) *string { return &p.relationsEdge }),
		intPref("t.int", 120, 40, 400, func(p *prefs) *int { return &p.ruler }),
		ValuePref("t.font", "mono", nil),
		ValuePref("t.size", "small", []string{"small", "large"}),
	}
	readRows(&p, map[string]any{"t.bool": "yes", "t.choice": "middle", "t.int": "39", "t.font": "", "t.size": "huge"}, rows)
	if !p.wrap || p.relationsEdge != "left" || p.ruler != 120 || p.Value("t.font") != "mono" || p.Value("t.size") != "small" {
		t.Fatalf("values it cannot use: %+v %v", p, p.values)
	}
	readRows(&p, map[string]any{"t.bool": "false", "t.choice": "top", "t.int": "400", "t.font": "Iosevka", "t.size": "large"}, rows)
	if p.wrap || p.relationsEdge != "top" || p.ruler != 400 || p.Value("t.font") != "Iosevka" || p.Value("t.size") != "large" {
		t.Fatalf("values it uses: %+v %v", p, p.values)
	}
	copied := p
	rows[3].read(&copied, "Fira")
	if p.Value("t.font") != "Iosevka" || copied.Value("t.font") != "Fira" {
		t.Errorf("a copy's change reached the original: %q %q", p.Value("t.font"), copied.Value("t.font"))
	}
	st := rowState(p, []PrefSpec{rows[1].chooser("App.tIndex", "App.setT"), rows[4].chooser("App.sizeIndex", "")})
	if st["App.tIndex"] != 2 || st["App.sizeIndex"] != 1 {
		t.Errorf("the choosers' rows: %v", st)
	}
}

// Two rows naming one preference, or one chooser, are refused when the registry is built.
func TestTheRegistryRefusesAPreferenceDeclaredTwice(t *testing.T) {
	a := &prefFake{fake: fake{name: "a"}, rows: []PrefSpec{ValuePref("t.font", "mono", nil).chooser("App.fontIndex", "App.setFont")}}
	for what, b := range map[string]*prefFake{
		"a name":    {fake: fake{name: "b"}, rows: []PrefSpec{ValuePref("t.font", "x", nil)}},
		"a chooser": {fake: fake{name: "b"}, rows: []PrefSpec{ValuePref("t.size", "x", nil).chooser("App.fontIndex", "")}},
		"a command": {fake: fake{name: "b"}, rows: []PrefSpec{ValuePref("t.size", "x", nil).chooser("", "App.setFont")}},
	} {
		if _, err := newRegistry(a, b); err == nil || !strings.Contains(err.Error(), "another names too") {
			t.Errorf("%s declared twice: %v", what, err)
		}
	}
	if _, err := newRegistry(a, &prefFake{fake: fake{name: "b"}, rows: []PrefSpec{ValuePref("t.size", "x", nil)}}); err != nil {
		t.Errorf("two features' own rows: %v", err)
	}
}

type prefFake struct {
	fake
	rows []PrefSpec
}

func (f *prefFake) Prefs() []PrefSpec { return f.rows }
