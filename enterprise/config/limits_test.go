package config_test

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
)

// withLimits is the good file with lines added to its first client (web-app), after its name.
func withLimits(lines string) string {
	return strings.Replace(good, "name         = \"web-app\"\n", "name         = \"web-app\"\n"+lines+"\n", 1)
}

// A client's limits load as written, and have their defaults: four searches in flight, no rate. A
// rate without a burst allows the rate, rounded up, at once, and at least one; a whole number is a
// rate too. The other client keeps its own defaults.
func TestAClientsLimitsLoad(t *testing.T) {
	s, err := load(t, good)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range s.HTTP.Clients {
		if c.MaxInFlight != config.DefaultMaxInFlight || c.Rate != 0 || c.Burst != 0 {
			t.Errorf("client %s's defaults: in flight %d, rate %v, burst %d; want %d, no rate", c.Name, c.MaxInFlight, c.Rate, c.Burst, config.DefaultMaxInFlight)
		}
	}
	for _, c := range []struct {
		lines    string
		inFlight int
		rate     float64
		burst    int
	}{
		{"max_in_flight = 8\nrate = 2.5\nburst = 10", 8, 2.5, 10},
		{"max_in_flight = 1", 1, 0, 0},
		{"max_in_flight = 256", 256, 0, 0},
		{"rate = 2.5", config.DefaultMaxInFlight, 2.5, 3},
		{"rate = 20", config.DefaultMaxInFlight, 20, 20},
		{"rate = 0.2", config.DefaultMaxInFlight, 0.2, 1},
		{"rate = 0.2\nburst = 5", config.DefaultMaxInFlight, 0.2, 5},
		{"rate = 0", config.DefaultMaxInFlight, 0, 0},
	} {
		s, err := load(t, withLimits(c.lines))
		if err != nil {
			t.Errorf("%q: %v", c.lines, err)
			continue
		}
		web, admin := s.HTTP.Clients[0], s.HTTP.Clients[1]
		if web.Name != "web-app" || web.MaxInFlight != c.inFlight || web.Rate != c.rate || web.Burst != c.burst {
			t.Errorf("%q: in flight %d, rate %v, burst %d; want %d, %v, %d", c.lines, web.MaxInFlight, web.Rate, web.Burst, c.inFlight, c.rate, c.burst)
		}
		if admin.MaxInFlight != config.DefaultMaxInFlight || admin.Rate != 0 || admin.Burst != 0 {
			t.Errorf("%q: the other client's limits moved: %+v", c.lines, admin)
		}
	}
}

// Each refusal of a client's limits names its field: searches in flight outside 1 to 256, a rate
// below 0 or above 10,000, a burst outside 1 to 100,000, and a burst with no rate to be the burst
// of. The mutants, named here: each bound removed in turn (its row loads).
func TestEveryLimitRefusalNamesItsField(t *testing.T) {
	for _, c := range []struct {
		lines, field, says string
	}{
		{"max_in_flight = 0", "http.client[0] (web-app).max_in_flight", "1 to 256"},
		{"max_in_flight = -1", "http.client[0] (web-app).max_in_flight", "1 to 256"},
		{"max_in_flight = 257", "http.client[0] (web-app).max_in_flight", "1 to 256"},
		{"rate = -0.5", "http.client[0] (web-app).rate", "0 (no rate) to 10000"},
		{"rate = 10000.5", "http.client[0] (web-app).rate", "0 (no rate) to 10000"},
		{"rate = nan", "http.client[0] (web-app).rate", "0 (no rate) to 10000"},
		{"rate = 2\nburst = 0", "http.client[0] (web-app).burst", "1 to 100000"},
		{"rate = 2\nburst = 100001", "http.client[0] (web-app).burst", "1 to 100000"},
		{"burst = 5", "http.client[0] (web-app).burst", "give rate too"},
		{"rate = 0\nburst = 5", "http.client[0] (web-app).burst", "give rate too"},
	} {
		_, err := load(t, withLimits(c.lines))
		if err == nil {
			t.Errorf("%q loaded", c.lines)
			continue
		}
		if !strings.Contains(err.Error(), c.field+": ") || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q: the refusal doesn't name %s with %q: %v", c.lines, c.field, c.says, err)
		}
	}
}
