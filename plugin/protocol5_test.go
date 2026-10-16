package plugin

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// PROTOCOL 5 — each cell's kind, on host.table and host.rows.

// TestProtocolFivesKindsRoundTrip: a table and its next rows written with kinds read back with
// them at protocol 5, and without them at 4, where a kinds field is not read at all; a table sent
// with an empty kinds list (no rows yet) reads as one with kinds, and one sent without them reads
// none. A row that is not a list is dropped with its kinds, so the others keep theirs.
func TestProtocolFivesKindsRoundTrip(t *testing.T) {
	wire := func(params []any) []any { return one(widen(params[0].(map[string]any)).(map[string]any)) }
	open := TableOpen{ID: "c1", Title: "Results", Fields: []string{"id", "name", "note"},
		Rows: [][]string{{"1", "ada", "NULL"}, {"12345678901234567890.123", "true", `{"a":1}`}}, Kinds: []string{"ntz", "nbj"}, More: true}
	if got, dropped, err := ReadTableAt(wire(TableParams(open)), 5); err != nil || dropped != 0 || !reflect.DeepEqual(got, open) {
		t.Errorf("host.table at 5: %+v (%d dropped), %v; want %+v", got, dropped, err, open)
	}
	got, _, err := ReadTableAt(wire(TableParams(open)), 4)
	if err != nil || got.Kinds != nil {
		t.Errorf("host.table at 4: kinds %q, %v; want none read", got.Kinds, err)
	}
	if got, _, _ := ReadTable(wire(TableParams(open))); got.Kinds != nil {
		t.Errorf("ReadTable (protocol 4) read kinds %q", got.Kinds)
	}
	empty := TableOpen{ID: "c2", Fields: []string{"a"}, Kinds: []string{}, More: true}
	if got, _, err := ReadTableAt(wire(TableParams(empty)), 5); err != nil || got.Kinds == nil || len(got.Kinds) != 0 {
		t.Errorf("a table with no rows and an empty kinds list: kinds %#v, %v; want an empty list", got.Kinds, err)
	}
	if got, _, err := ReadTableAt(wire(TableParams(TableOpen{ID: "c3", Fields: []string{"a"}})), 5); err != nil || got.Kinds != nil {
		t.Errorf("a table sent without kinds: %#v, %v", got.Kinds, err)
	}

	rows := [][]string{{"3", "bo", "x"}}
	r, dropped, err := ReadTableRows(wire(RowsKindsParams("c1", rows, []string{"ntt"}, false)), 5)
	if err != nil || dropped != 0 || !reflect.DeepEqual(r, TableRows{ID: "c1", Rows: rows, Kinds: []string{"ntt"}}) {
		t.Errorf("host.rows at 5: %+v (%d dropped) %v", r, dropped, err)
	}
	if r, _, err := ReadTableRows(wire(RowsKindsParams("c1", rows, []string{"ntt"}, false)), 4); err != nil || r.Kinds != nil {
		t.Errorf("host.rows at 4: kinds %q, %v; want none read", r.Kinds, err)
	}
	if r, _, _ := ReadTableRows(wire(RowsParams("c1", rows, true)), 5); r.Kinds != nil || !r.More {
		t.Errorf("host.rows without kinds at 5: %+v", r)
	}

	// the row that is not a list goes with its kinds; the next keeps its own
	r, dropped, err = ReadTableRows(one(map[string]any{"id": "c1", "rows": []any{"no", []any{"a"}}, "kinds": []any{"n", "j"}}), 5)
	if err != nil || dropped != 1 || !reflect.DeepEqual(r.Rows, [][]string{{"a"}}) || !reflect.DeepEqual(r.Kinds, []string{"j"}) {
		t.Errorf("a dropped row's kinds: %+v (%d dropped) %v", r, dropped, err)
	}
	// at protocol 4 a malformed kinds field is not read, so it refuses nothing
	if _, _, err := ReadTableRows(one(map[string]any{"id": "c1", "rows": []any{[]any{"a"}}, "kinds": "junk"}), 4); err != nil {
		t.Errorf("a protocol-4 page refused for a kinds field it does not read: %v", err)
	}
}

// TestAKindsStringThatDoesNotFitIsMalformed: at protocol 5 a table whose kinds string is shorter or
// longer than its fields, holds a byte that is no kind, or whose kinds are not one string per row,
// is malformed and refused whole; host.rows' kinds are refused when they are not one string per row
// (their length is the host's to check, against the table's fields, with CheckKinds).
func TestAKindsStringThatDoesNotFitIsMalformed(t *testing.T) {
	table := func(kinds any) []any {
		return one(map[string]any{"id": "x", "fields": []any{"a", "b"}, "rows": []any{[]any{"1", "2"}}, "kinds": kinds})
	}
	for name, kinds := range map[string]any{
		"short":          []any{"n"},
		"long":           []any{"nnn"},
		"not a kind":     []any{"nx"},
		"an upper kind":  []any{"nT"},
		"two for a row":  []any{"nn", "nn"},
		"none for a row": []any{},
		"not strings":    []any{int64(3)},
		"not a list":     "nn",
	} {
		if _, _, err := ReadTableAt(table(kinds), 5); !errors.Is(err, ErrParams) {
			t.Errorf("host.table, kinds %s: %v, want ErrParams", name, err)
		}
	}
	if _, _, err := ReadTableAt(table([]any{"nb"}), 5); err != nil {
		t.Errorf("kinds that fit: %v", err)
	}
	rows := func(kinds any) []any {
		return one(map[string]any{"id": "x", "rows": []any{[]any{"1", "2"}}, "kinds": kinds})
	}
	for name, kinds := range map[string]any{"two for a row": []any{"nn", "nn"}, "not strings": []any{true}, "not a list": "nn"} {
		if _, _, err := ReadTableRows(rows(kinds), 5); !errors.Is(err, ErrParams) {
			t.Errorf("host.rows, kinds %s: %v, want ErrParams", name, err)
		}
	}
	for _, c := range []struct {
		kinds        []string
		rows, fields int
		why          string
	}{
		{[]string{"tz", "nb"}, 2, 2, ""},
		{[]string{"j"}, 1, 1, ""},
		{nil, 0, 3, ""},
		{[]string{"t"}, 1, 2, "row 0's kinds are 1 bytes for 2 fields"},
		{[]string{"ttt"}, 1, 2, "row 0's kinds are 3 bytes for 2 fields"},
		{[]string{"tt", "t?"}, 2, 2, `row 1's kinds hold '?', not a kind`},
		{[]string{"tt"}, 2, 2, "1 kinds strings for 2 rows"},
	} {
		if why := CheckKinds(c.kinds, c.rows, c.fields); why != c.why {
			t.Errorf("CheckKinds(%q, %d, %d) = %q, want %q", c.kinds, c.rows, c.fields, why, c.why)
		}
	}
}

// kinder is a protocol-5 plugin serving a table with kinds.
type kinder struct{ tabler }

func (k *kinder) Command(string) {
	_ = k.peer.Table(TableOpen{ID: "c1", Fields: []string{"n"}, Rows: [][]string{{"0"}}, Kinds: []string{"n"}, More: true})
}
func (k *kinder) FetchRows(f TableFetch) {
	_ = k.peer.RowsKinds(f.ID, [][]string{{"NULL"}}, []string{"z"}, false)
}

// TestThePeerSendsKindsOnlyAtProtocolFive: a host that opened the plugin at 5 is sent the table's
// and the rows' kinds; one at 4 is sent the same table and rows without them.
func TestThePeerSendsKindsOnlyAtProtocolFive(t *testing.T) {
	for _, protocol := range []int{5, 4} {
		hs := start(t, &kinder{})
		hs.send(t, MethodOpen, OpenParams(Open{Protocol: protocol}))
		hs.next(t)
		hs.send(t, MethodCommand, CommandParams("run"))
		table := hs.next(t)
		_, hasTable := table.params[0].(map[string]any)["kinds"]
		hs.send(t, MethodFetch, FetchParams(TableFetch{ID: "c1", N: 1}))
		rows := hs.next(t)
		_, hasRows := rows.params[0].(map[string]any)["kinds"]
		if hasTable != (protocol == 5) || hasRows != (protocol == 5) {
			t.Errorf("protocol %d: kinds on host.table %v, on host.rows %v", protocol, hasTable, hasRows)
		}
		if protocol == 5 {
			got, _, err := ReadTableRows(rows.params, 5)
			if err != nil || strings.Join(got.Kinds, ",") != "z" {
				t.Errorf("the rows' kinds: %q, %v", got.Kinds, err)
			}
		}
		hs.send(t, MethodClose, EmptyParams())
		if err := hs.served(t); err != nil {
			t.Fatal(err)
		}
	}
}
