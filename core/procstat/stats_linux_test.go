package procstat

import "testing"

func TestResidentPages(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint64
	}{{"100 25 3 1 0 9 0\n", 25}, {"100", 0}, {"", 0}, {"1 x", 0}} {
		if got := residentPages([]byte(c.in)); got != c.want {
			t.Errorf("residentPages(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
