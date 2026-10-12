package tui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
)

// PREFERENCE ROWS — one row per preference: its name in the store, the values it takes and its
// default, where its value lives, what the document reads of it, and the Preferences dialog's
// chooser for it. Reading the store (prefsFor), the dialog's choosers (their index sources and
// set commands) and the write to the store all go by the rows, so a preference is declared once.
//
// A feature declares its own rows (the Prefs capability), each a ValuePref read with
// prefs.Value. Composite preferences — a plugin's placement and keys, the panels' geometry, the
// agents, the recent files, the search's stages — keep readers of their own.

// PrefSpec is one preference.
type PrefSpec struct {
	Name    string   // the store's key: "tui.editor.wrap"
	Choices []string // the stored values a chooser offers, in its rows' order; a yes/no one: "true", "false"
	Index   string   // the App.* source a chooser reads: the value's row; "" for no chooser
	Set     string   // the App.* command a chooser calls with a row; "" for none
	// State is what the document reads of the value, beyond the chooser's row; nil for nothing.
	State func(p prefs) map[string]any
	// Changed runs after the user chose a new value (a panel docks again, a notice); nil for nothing.
	Changed func(p prefs)

	valid func(v string) bool      // a stored value the TUI can use; nil: one of Choices
	read  func(p *prefs, v string) // a usable value into p
	value func(p prefs) string     // p's value, as stored
	reset func(p *prefs)           // the default into p
}

// Prefs is a feature that declares preferences of its own.
type Prefs interface{ Prefs() []PrefSpec }

// admits reports whether v is a value the TUI can use.
func (s PrefSpec) admits(v string) bool {
	if s.valid != nil {
		return s.valid(v)
	}
	return slices.Contains(s.Choices, v)
}

// row is the chooser's row of p's value: its place in Choices, the first row for one not there.
func (s PrefSpec) row(p prefs) int { return max(slices.Index(s.Choices, s.value(p)), 0) }

// chooser gives the row the Preferences dialog's chooser: the source it reads and the command it calls.
func (s PrefSpec) chooser(index, set string) PrefSpec { s.Index, s.Set = index, set; return s }

// shows gives the row what the document reads of its value.
func (s PrefSpec) shows(state func(p prefs) map[string]any) PrefSpec { s.State = state; return s }

// then gives the row what follows a change the user makes.
func (s PrefSpec) then(changed func(p prefs)) PrefSpec { s.Changed = changed; return s }

// boolPref is a yes-or-no preference kept as "true" or "false", yes first in its chooser.
func boolPref(name string, def bool, field func(*prefs) *bool) PrefSpec {
	return PrefSpec{Name: name, Choices: []string{"true", "false"},
		read:  func(p *prefs, v string) { *field(p) = v == "true" },
		value: func(p prefs) string { return strconv.FormatBool(*field(&p)) },
		reset: func(p *prefs) { *field(p) = def }}
}

// choicePref is one of a list of names.
func choicePref(name, def string, choices []string, field func(*prefs) *string) PrefSpec {
	return PrefSpec{Name: name, Choices: choices,
		read:  func(p *prefs, v string) { *field(p) = v },
		value: func(p prefs) string { return *field(&p) },
		reset: func(p *prefs) { *field(p) = def }}
}

// intPref is a whole number from lo to hi. Its chooser, when it has one, offers each of them.
func intPref(name string, def, lo, hi int, field func(*prefs) *int) PrefSpec {
	return PrefSpec{Name: name,
		valid: func(v string) bool { n, err := strconv.Atoi(v); return err == nil && n >= lo && n <= hi },
		read:  func(p *prefs, v string) { *field(p), _ = strconv.Atoi(v) },
		value: func(p prefs) string { return strconv.Itoa(*field(&p)) },
		reset: func(p *prefs) { *field(p) = def }}
}

// ValuePref is a feature's own preference, one of choices (any value but "" when choices is nil),
// read with prefs.Value.
func ValuePref(name, def string, choices []string) PrefSpec {
	s := PrefSpec{Name: name, Choices: choices,
		read: func(p *prefs, v string) {
			p.values = maps.Clone(p.values) // a prefs is a value: its copies never share a change
			if p.values == nil {
				p.values = map[string]string{}
			}
			p.values[name] = v
		},
		value: func(p prefs) string { return p.Value(name) },
	}
	s.reset = func(p *prefs) { s.read(p, def) }
	if choices == nil {
		s.valid = func(v string) bool { return v != "" }
	}
	return s
}

// Value is a feature's preference (ValuePref), by its name.
func (p prefs) Value(name string) string { return p.values[name] }

// readRows reads the store's values into p, over the rows' defaults; a value the TUI cannot use
// is the default's.
func readRows(p *prefs, m map[string]any, rows []PrefSpec) {
	for _, s := range rows {
		s.reset(p)
		if v, ok := m[s.Name].(string); ok && v != "" && s.admits(v) {
			s.read(p, v)
		}
	}
}

// rowState is what the document reads of the rows' values: each chooser's row, and each row's own.
func rowState(p prefs, rows []PrefSpec) map[string]any {
	m := map[string]any{}
	for _, s := range rows {
		if s.Index != "" {
			m[s.Index] = s.row(p)
		}
		if s.State != nil {
			maps.Copy(m, s.State(p))
		}
	}
	return m
}

// checkRows refuses two rows of one name, and a chooser or a command two rows share.
func checkRows(rows []PrefSpec) error {
	seen := map[string]bool{}
	for _, s := range rows {
		for _, k := range []string{s.Name, s.Index, s.Set} {
			if k != "" && seen[k] {
				return fmt.Errorf("tui: the preference %s names %s, which another names too", s.Name, k)
			}
			seen[k] = true
		}
	}
	return nil
}

// prefChoosers is the feature the preferences' choosers belong to: every row's index source and
// set command, the host's own rows and the features'.
type prefChoosers struct{ h *Host }

func (prefChoosers) Name() string { return "preferences" }

func (c prefChoosers) State() map[string]any { return rowState(c.h.prefs, c.h.prefRows) }

func (c prefChoosers) Commands() map[string]Command {
	m := map[string]Command{}
	for _, s := range c.h.prefRows {
		if s.Set != "" {
			m[s.Set] = oneNumber("a row", func(i int) { c.h.choosePref(s, i) })
		}
	}
	return m
}

// choosePref is a chooser's row i chosen: the row's value, on screen and kept, then what follows
// a change of it. The value in effect is kept too, when chosen: it may be a default the store does
// not hold, and the user who chose it keeps it when the default changes. A row out of range
// changes nothing.
func (h *Host) choosePref(s PrefSpec, i int) {
	if i < 0 || i >= len(s.Choices) {
		return
	}
	v := s.Choices[i]
	changed := v != s.value(h.prefs)
	h.setPref(s.Name, v, func(p *prefs) { s.read(p, v) })
	if changed && s.Changed != nil {
		s.Changed(h.prefs)
	}
}

// setRowPref sets row's preference to v through the row alone (Core.SetPref): read into the live
// prefs, its State published and its Changed run when v changed, then stored in order. A row
// that a feature's code sets (the recent files on every open) does not re-apply every preference,
// as a choice in the dialog does (choosePref).
func (h *Host) setRowPref(row PrefSpec, v string) {
	changed := v != row.value(h.prefs)
	p := h.prefs
	row.read(&p, v)
	h.prefs = p
	if row.State != nil {
		h.keep(h.p.SetMany(row.State(p)))
	}
	if changed && row.Changed != nil {
		row.Changed(p)
	}
	h.storePref(row.Name, v)
}

// choosePrefNamed is choosePref by the preference's name: the menu's and the tests' way to a row.
func (h *Host) choosePrefNamed(name string, i int) {
	for _, s := range h.prefRows {
		if s.Name == name {
			h.choosePref(s, i)
			return
		}
	}
}
