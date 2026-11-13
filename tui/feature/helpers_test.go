package feature

import "testing"

// Fill fills a sentence in one pass: an argument that says %1 or %2 itself (a path, a command, a
// search) is shown as written, never filled again; a %N past the arguments stays, as Qt's arg.
func TestFillIsOnePass(t *testing.T) {
	for _, c := range []struct {
		s    string
		args []any
		want string
	}{
		{"%1 is taken by %2", []any{"%2", "x"}, "%2 is taken by x"},
		{"open %1 in %2", []any{"a", "%1%2"}, "open a in %1%2"},
		{"settling %1%", []any{40}, "settling 40%"},
		{"%2 before %1", []any{"a", "b"}, "b before a"},
		{"%10 of %1", []any{"a"}, "a0 of a"},
		{"%3 stays", []any{"a"}, "%3 stays"},
		{"no arguments: %1", nil, "no arguments: %1"},
	} {
		if got := Fill(c.s, c.args...); got != c.want {
			t.Errorf("Fill(%q, %v) = %q, want %q", c.s, c.args, got, c.want)
		}
	}
}
