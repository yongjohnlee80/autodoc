package feature

import (
	"fmt"
	"maps"
	"slices"
)

// PREFERENCE ROWS — one row per preference: its name in the store, the values it takes and its
// default, where its value lives, what the document reads of it, and the Preferences dialog's
// chooser for it. Reading the store (prefsFor), the dialog's choosers (their index sources and
// set commands) and the write to the store all go by the rows, so a preference is declared once.
//
// A feature declares its own rows (the Prefs capability), each a ValuePref, and reads any row by
// its name through PrefsView.Value. Composite preferences — a plugin's placement and keys, the
// panels' geometry, the agents, the recent files, the search's stages — keep readers of their own.

// PrefsView is the preferences as a feature reads them: any row's value by its name, and a panel's
// size and length at an edge. Nothing else of the host's preferences is the kit's to name; a
// feature that needs more reads it through its own Host interface.
type PrefsView interface {
	Value(name string) string
	PanelGeometry(id, edge string) (size, length int)
}

// PrefsWriter is the preferences a row's value is read into: the host's own, which keeps each
// row's value where its binding says (PrefBinding). A feature never holds one: Core.Prefs is a
// PrefsView, a snapshot.
type PrefsWriter interface {
	PrefsView
	SetValue(name, v string) // a ValuePref's value, by its name
}

// PrefBinding is where a row's value lives and how it is checked: the host's bindings of its own
// rows to its preferences' fields (BoundPref). ValuePref binds a feature's row by its name.
type PrefBinding struct {
	Valid func(v string) bool           // a stored value the TUI can use; nil: one of the row's Choices
	Read  func(p PrefsWriter, v string) // a usable value into p
	Value func(p PrefsView) string      // p's value, as stored
	Reset func(p PrefsWriter)           // the default into p
}

// BoundPref is the row name, one of choices (nil when Valid says what it takes), kept where b says.
func BoundPref(name string, choices []string, b PrefBinding) PrefSpec {
	return PrefSpec{Name: name, Choices: choices, Valid: b.Valid, Read: b.Read, Value: b.Value, Reset: b.Reset}
}

// PrefSpec is one preference.
type PrefSpec struct {
	Name    string   // the store's key: "tui.editor.wrap"
	Choices []string // the stored values a chooser offers, in its rows' order; a yes/no one: "true", "false"
	Index   string   // the App.* source a chooser reads: the value's row; "" for no chooser
	Set     string   // the App.* command a chooser calls with a row; "" for none
	// State is what the document reads of the value, beyond the chooser's row; nil for nothing.
	State func(p PrefsView) map[string]any
	// Changed runs after the user chose a new value (a panel docks again, a notice); nil for nothing.
	Changed func(p PrefsView)

	Valid  func(v string) bool           // a stored value the TUI can use; nil: one of Choices
	Read   func(p PrefsWriter, v string) // a usable value into p
	Value  func(p PrefsView) string      // p's value, as stored
	Reset  func(p PrefsWriter)           // the default into p
	ByName bool                          // a ValuePref: its value is kept by its name, not in a field
}

// Prefs is a feature that declares preferences of its own.
type Prefs interface{ Prefs() []PrefSpec }

// admits reports whether v is a value the TUI can use.
func (s PrefSpec) admits(v string) bool {
	if s.Valid != nil {
		return s.Valid(v)
	}
	return slices.Contains(s.Choices, v)
}

// row is the chooser's row of p's value: its place in Choices, the first row for one not there.
func (s PrefSpec) Row(p PrefsView) int { return max(slices.Index(s.Choices, s.Value(p)), 0) }

// chooser gives the row the Preferences dialog's chooser: the source it reads and the command it calls.
func (s PrefSpec) Chooser(index, set string) PrefSpec { s.Index, s.Set = index, set; return s }

// shows gives the row what the document reads of its value.
func (s PrefSpec) Shows(state func(p PrefsView) map[string]any) PrefSpec { s.State = state; return s }

// then gives the row what follows a change the user makes.
func (s PrefSpec) Then(changed func(p PrefsView)) PrefSpec { s.Changed = changed; return s }

// ValuePref is a feature's own preference, one of choices (any value but "" when choices is nil),
// read with PrefsView.Value.
func ValuePref(name, def string, choices []string) PrefSpec {
	s := PrefSpec{Name: name, Choices: choices, ByName: true,
		Read:  func(p PrefsWriter, v string) { p.SetValue(name, v) },
		Value: func(p PrefsView) string { return p.Value(name) },
	}
	s.Reset = func(p PrefsWriter) { s.Read(p, def) }
	if choices == nil {
		s.Valid = func(v string) bool { return v != "" }
	}
	return s
}

// ReadRows reads the store's values into p, over the rows' defaults; a value the TUI cannot use
// is the default's.
func ReadRows(p PrefsWriter, m map[string]any, rows []PrefSpec) {
	for _, s := range rows {
		s.Reset(p)
		if v, ok := m[s.Name].(string); ok && v != "" && s.admits(v) {
			s.Read(p, v)
		}
	}
}

// RowState is what the document reads of the rows' values: each chooser's row, and each row's own.
func RowState(p PrefsView, rows []PrefSpec) map[string]any {
	m := map[string]any{}
	for _, s := range rows {
		if s.Index != "" {
			m[s.Index] = s.Row(p)
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
