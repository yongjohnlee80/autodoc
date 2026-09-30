package plugin

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestAMalformedNotificationIsRefused: parameters that are not the one map the protocol carries, or
// lack what a notification needs, are ErrParams.
func TestAMalformedNotificationIsRefused(t *testing.T) {
	for name, read := range map[string]func([]any) error{
		"open":        func(p []any) error { _, err := ReadOpen(p); return err },
		"key":         func(p []any) error { _, err := ReadKey(p); return err },
		"resize":      func(p []any) error { _, _, err := ReadResize(p); return err },
		"theme":       func(p []any) error { _, err := ReadTheme(p); return err },
		"ready":       func(p []any) error { _, err := ReadReady(p); return err },
		"title":       func(p []any) error { _, err := ReadTitle(p); return err },
		"frame":       func(p []any) error { _, _, err := ReadFrame(p); return err },
		"two maps":    func(p []any) error { _, err := ReadKey([]any{map[string]any{}, map[string]any{}}); return err },
		"not a map":   func(p []any) error { _, err := ReadKey([]any{"key"}); return err },
		"open, sizes": func(p []any) error { _, err := ReadOpen(one(map[string]any{"protocol": int64(1)})); return err },
		"resize, w":   func(p []any) error { _, _, err := ReadResize(one(map[string]any{"height": int64(3)})); return err },
		"ready, p":    func(p []any) error { _, err := ReadReady(EmptyParams()); return err },
		"frame, rows": func(p []any) error { _, _, err := ReadFrame(EmptyParams()); return err },
	} {
		if err := read(nil); !errors.Is(err, ErrParams) {
			t.Errorf("%s: %v, want ErrParams", name, err)
		}
	}
}

// TestNumbersReadAsTheDecoderGivesThem: msgpack decodes a number as int64, or uint64 past it; a
// uint64 too large for a size is refused.
func TestNumbersReadAsTheDecoderGivesThem(t *testing.T) {
	for _, c := range []struct {
		v  any
		n  int
		ok bool
	}{{int64(7), 7, true}, {uint64(9), 9, true}, {7, 7, true}, {uint64(1 << 40), 0, false}, {"7", 0, false}} {
		n, ok := num(map[string]any{"n": c.v}, "n")
		if ok != c.ok || (ok && n != c.n) {
			t.Errorf("num(%#v) = %d, %v; want %d, %v", c.v, n, ok, c.n, c.ok)
		}
	}
}

// TestAFrameIsClippedAndItsMalformedRunsDropped: past MaxRows and MaxRunsPerRow a frame is cut, a
// run that is not a map with text is dropped and counted, and the rest arrives.
func TestAFrameIsClippedAndItsMalformedRunsDropped(t *testing.T) {
	rows := make([]any, MaxRows+5)
	for i := range rows {
		rows[i] = []any{map[string]any{"t": "x"}}
	}
	long := make([]any, MaxRunsPerRow+3)
	for i := range long {
		long[i] = map[string]any{"t": "y", "fg": "red", "u": true}
	}
	rows[0] = long
	rows[1] = []any{map[string]any{"t": "ok", "i": true}, "not a run", map[string]any{"fg": "red"}}
	rows[2] = "not a row"
	got, dropped, err := ReadFrame(one(map[string]any{"rows": rows}))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxRows || len(got[0]) != MaxRunsPerRow {
		t.Fatalf("clipped to %d rows, %d runs; want %d, %d", len(got), len(got[0]), MaxRows, MaxRunsPerRow)
	}
	if got[0][0] != (Run{Text: "y", Style: Style{FG: "red", Underline: true}}) {
		t.Errorf("run = %#v", got[0][0])
	}
	if !reflect.DeepEqual(got[1], Row{{Text: "ok", Style: Style{Italic: true}}}) || got[2] != nil {
		t.Errorf("rows 1, 2 = %#v, %#v", got[1], got[2])
	}
	if dropped != 3 {
		t.Errorf("dropped = %d, want 3 (two runs, one row)", dropped)
	}
}

// TestAFrameIsItsCellsRunTogether: a frame's rows are its cells, run together where the style is the
// same; writes outside it are dropped.
func TestAFrameIsItsCellsRunTogether(t *testing.T) {
	f := NewFrame(5, 2)
	red := Style{FG: "red"}
	if x := f.Text(3, 0, "abc", red); x != 6 {
		t.Errorf("Text returned %d, want 6 (past the edge)", x)
	}
	f.Fill(0, 1, 2, 5, '#', Style{BG: "blue"})
	f.Set(-1, 0, 'z', red)
	if w, h := f.Size(); w != 5 || h != 2 {
		t.Errorf("Size = %d, %d", w, h)
	}
	want := []Row{
		{{Text: "   "}, {Text: "ab", Style: red}},
		{{Text: "##", Style: Style{BG: "blue"}}, {Text: "   "}},
	}
	if got := f.Rows(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Rows:\n got %#v\nwant %#v", got, want)
	}
	if empty := NewFrame(-1, 3).Rows(); len(empty) != 3 || len(empty[0]) != 0 {
		t.Errorf("a frame no wide = %#v", empty)
	}
	if s := FrameParams(want); !strings.Contains(stringOf(s), "blue") {
		t.Errorf("FrameParams lost a style: %v", s)
	}
}

func stringOf(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				b.WriteString(k)
				walk(e)
			}
		case string:
			b.WriteString(x)
		}
	}
	walk(v)
	return b.String()
}
