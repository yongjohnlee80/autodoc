package plugin

import "testing"

// TestKeyNamesAndCodesAreOneTable: every named key's code gives its name back, and its name its
// code; no two keys share a name. The host sends by KeyName and plugin/decl reads by KeyCode.
func TestKeyNamesAndCodesAreOneTable(t *testing.T) {
	if len(keyCodes) != len(keyNames) {
		t.Fatalf("%d names for %d codes: two keys share a name", len(keyCodes), len(keyNames))
	}
	for code, name := range keyNames {
		if back, ok := KeyCode(name); !ok || back != code || KeyName(code) != name {
			t.Errorf("%q: KeyCode = %v, %v; KeyName(%v) = %q", name, back, ok, code, KeyName(code))
		}
	}
	if _, ok := KeyCode("Esc"); ok {
		t.Error("Esc has a code: the host never sends it")
	}
	if KeyName('a') != "" {
		t.Error("a printable key has a name: it is sent as its text")
	}
}
