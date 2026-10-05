package index

import "testing"

// TestDocumentsFilters: the path, missing-field and date rules index.documents narrows and orders by.
func TestDocumentsFilters(t *testing.T) {
	for _, tc := range []struct {
		path     string
		prefixes []string
		want     bool
	}{
		{"adrs/a.md", nil, true},
		{"adrs/a.md", []string{"adrs"}, true},
		{"adrs/a.md", []string{"adrs/"}, true},
		{"adrs/a.md", []string{"adrs/a.md"}, true},
		{"adrsx/a.md", []string{"adrs"}, false},
		{"notes/a.md", []string{"adrs", "notes"}, true},
		{"a.md", []string{"."}, true},
	} {
		if got := underAny(tc.path, tc.prefixes); got != tc.want {
			t.Errorf("underAny(%q, %q) = %v", tc.path, tc.prefixes, got)
		}
	}
	front := map[string]any{"abstract": "Yes.", "tags": []any{}, "blank": "  ", "status": "x", "list": []any{"a"}}
	for _, tc := range []struct {
		fields []string
		want   bool
	}{
		{nil, true},
		{[]string{"abstract"}, false},
		{[]string{"tags"}, true}, // an empty list is missing
		{[]string{"blank"}, true},
		{[]string{"absent"}, true},
		{[]string{"list", "status"}, false},
		{[]string{"status", "absent"}, true},
	} {
		if got := missesOne(front, tc.fields); got != tc.want {
			t.Errorf("missesOne(%q) = %v", tc.fields, got)
		}
	}
	for _, v := range []any{"2026-10-05", "2026-10-05T10:00:00Z"} {
		if _, ok := frontDate(v); !ok {
			t.Errorf("frontDate(%v) not read", v)
		}
	}
	if _, ok := frontDate("next week"); ok {
		t.Error("frontDate read a non-date")
	}
}
