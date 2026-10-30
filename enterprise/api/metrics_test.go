package api

import (
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// The metrics exposition: GET /v1/metrics, off by default, an operator's as
// /v1/status is, for its tenants, in Prometheus's text format, with no secret or private text in
// any series. The mutants, named here: the auth skipped (a client or no token reads it); the
// tenant filter off (an operator sees another's tenant); the totals reset with the metrics
// lines' interval; error text as a label; the raw path as a label.

// parse reads body strictly as the text exposition: every family has HELP and TYPE, histograms
// are well formed, and no sample is malformed.
func parse(t *testing.T, body string) map[string]*familyView {
	t.Helper()
	// the legacy scheme: classic metric and label names, the strictest the parser knows
	p := expfmt.NewTextParser(model.LegacyValidation)
	fams, err := p.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("the exposition does not parse: %v\n%s", err, body)
	}
	out := map[string]*familyView{}
	for name, f := range fams {
		if f.GetHelp() == "" {
			t.Errorf("%s has no HELP", name)
		}
		v := &familyView{samples: map[string]float64{}}
		for _, m := range f.GetMetric() {
			var labels []string
			for _, l := range m.GetLabel() {
				labels = append(labels, l.GetName()+"="+l.GetValue())
			}
			sort.Strings(labels)
			key := strings.Join(labels, ",")
			switch {
			case m.Gauge != nil:
				v.samples[key] = m.GetGauge().GetValue()
			case m.Counter != nil:
				v.samples[key] = m.GetCounter().GetValue()
			case m.Histogram != nil:
				v.samples[key+",count"] = float64(m.GetHistogram().GetSampleCount())
				prev := uint64(0)
				for _, b := range m.GetHistogram().GetBucket() {
					if b.GetCumulativeCount() < prev {
						t.Errorf("%s{%s}: buckets not cumulative", name, key)
					}
					prev = b.GetCumulativeCount()
				}
			}
		}
		out[name] = v
	}
	return out
}

type familyView struct{ samples map[string]float64 }

func TestMetricsAreOffByDefault(t *testing.T) {
	srv := opsAPI(t, nil, nil)
	if r := get(t, srv, MetricsPath, opsToken); r.status != 404 {
		t.Fatalf("metrics with WithMetrics off: %d, want 404", r.status)
	}
}

func TestMetricsAreForOperatorsOnly(t *testing.T) {
	srv := opsAPIWith(t, nil, nil, WithMetrics(true))
	if r := get(t, srv, MetricsPath, ""); r.status != 401 {
		t.Errorf("no token: %d, want 401", r.status)
	}
	if r := get(t, srv, MetricsPath, clientA); r.status != 403 {
		t.Errorf("a search client: %d, want 403", r.status)
	}
	r := get(t, srv, MetricsPath, opsToken)
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("an operator: %d %q", r.status, r.header.Get("Content-Type"))
	}
	fams := parse(t, string(r.body))
	for _, name := range []string{"autodoc_server_info", "autodoc_provider_up", "autodoc_http_requests_total",
		"autodoc_http_request_duration_seconds", "autodoc_tenant_documents", "autodoc_tenant_lock_held", "autodoc_tenant_errors"} {
		if fams[name] == nil {
			t.Errorf("no %s", name)
		}
	}
	if got := fams["autodoc_tenant_documents"].samples["state=total,tenant=music"]; got != 7 {
		t.Errorf("music's documents: %v, want 7", got)
	}
	if got := fams["autodoc_tenant_errors"].samples["kind=pass,tenant=music"]; got != 1 {
		t.Errorf("music's last pass failed (a view error): %v, want 1", got)
	}
}

func TestMetricsShowTheOperatorsTenantsOnly(t *testing.T) {
	srv := opsAPIWith(t, nil, nil, WithMetrics(true))
	body := string(get(t, srv, MetricsPath, otherOps).body)
	if !strings.Contains(body, `tenant="other"`) || strings.Contains(body, `tenant="music"`) {
		t.Fatalf("the operator of other saw:\n%s", body)
	}
}

// The totals are monotonic, whatever the metrics lines take per interval; a path the API doesn't
// answer is counted as "other", never by its own name.
func TestMetricsTotalsAreCumulativeAndPathsClassified(t *testing.T) {
	srv := opsAPIWith(t, nil, nil, WithMetrics(true))
	get(t, srv, "/v1/health", "")
	get(t, srv, "/no/such/path-marker-zzz", "")
	first := parse(t, string(get(t, srv, MetricsPath, opsToken).body))["autodoc_http_requests_total"]
	// the metrics lines take their interval's counts: the totals stay
	srv.Config.Handler.(*Handler).TakeCounts()
	get(t, srv, "/v1/health", "")
	second := parse(t, string(get(t, srv, MetricsPath, opsToken).body))["autodoc_http_requests_total"]
	if second.samples["class=2xx,path=health"] != first.samples["class=2xx,path=health"]+1 {
		t.Errorf("health 2xx: %v then %v, want one more", first.samples["class=2xx,path=health"], second.samples["class=2xx,path=health"])
	}
	if first.samples["class=4xx,path=other"] < 1 {
		t.Errorf("the unknown path is not counted as other: %+v", first.samples)
	}
	// the first scrape is counted once it has answered: the second sees it
	if second.samples["class=2xx,path=metrics"] < 1 {
		t.Errorf("a scrape isn't counted on the next one: %+v", second.samples)
	}
	if strings.Contains(string(get(t, srv, MetricsPath, opsToken).body), "path-marker-zzz") {
		t.Error("a raw URL path became a label")
	}
}

// Nothing private reaches a series: no token, no error text. A tenant whose state can't be read
// shows its error, not a false 0 of documents.
func TestMetricsHoldNothingPrivateAndNoFalseZero(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	srv := opsAPIWith(t, &down, nil, WithMetrics(true))
	body := string(get(t, srv, MetricsPath, opsToken).body)
	for _, secret := range []string{opsToken, clientA, markerError, "track_marker_xyz"} {
		if strings.Contains(body, secret) {
			t.Fatalf("the exposition holds %q:\n%s", secret, body)
		}
	}
	fams := parse(t, body)
	if got := fams["autodoc_tenant_errors"].samples["kind=tenant,tenant=music"]; got != 1 {
		t.Errorf("music's state unreadable: errors{kind=tenant} %v, want 1", got)
	}
	if d := fams["autodoc_tenant_documents"]; d != nil {
		if _, ok := d.samples["state=total,tenant=music"]; ok {
			t.Errorf("music's documents shown while its state can't be read: %+v", d.samples)
		}
	}
}
