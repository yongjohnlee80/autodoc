package ops

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// THE METRICS EXPOSITION: GET /v1/metrics in Prometheus's text format 0.0.4,
// written here by hand from the status snapshot and the API's cumulative totals. No token, query
// text, client name, access value or error text is in any series: an error is a 0/1 by kind, and
// its text stays in /v1/status and the log. Paths and classes are fixed sets, and tenants are the
// configured ones, so the series are bounded by configuration.

// MetricsContentType is the exposition's media type.
const MetricsContentType = "text/plain; version=0.0.4; charset=utf-8"

// The paths a request is counted under: the API's four, and every other one as "other", so no raw
// URL path ever becomes a label.
const (
	PathSearch  = "search"
	PathStatus  = "status"
	PathMetrics = "metrics"
	PathHealth  = "health"
	PathOther   = "other"
)

// MetricPaths are the paths, in the order they are written.
var MetricPaths = []string{PathSearch, PathStatus, PathMetrics, PathHealth, PathOther}

// The status classes a request is counted under.
const (
	Class2xx = "2xx"
	Class4xx = "4xx"
	Class5xx = "5xx"
)

// MetricClasses are the classes, in the order they are written.
var MetricClasses = []string{Class2xx, Class4xx, Class5xx}

// DurationBuckets are the request-duration histogram's upper bounds, in seconds.
var DurationBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// ClassOf is a status's class.
func ClassOf(status int) string {
	switch {
	case status >= 500:
		return Class5xx
	case status >= 400:
		return Class4xx
	}
	return Class2xx
}

// PathTotals are one path's requests since the server started: by class, and their durations as
// a histogram (Buckets[i] counts the requests no longer than DurationBuckets[i]; the last, +Inf,
// is Count).
type PathTotals struct {
	ByClass map[string]uint64
	Buckets []uint64
	Count   uint64
	Sum     time.Duration
}

// HTTPTotals are the API's requests since the server started, by path: cumulative, never reset,
// so a scraper can take rates of them (the metrics lines' HTTPCounts are per interval).
type HTTPTotals map[string]PathTotals

// WriteMetrics writes s and totals as the exposition: the server's series, then each tenant's.
// A tenant whose state could not be read (its Error set) has only its error and lock series: its
// documents and quarantine are absent, never a false 0.
func WriteMetrics(w io.Writer, s Snapshot, totals HTTPTotals) error {
	b := &strings.Builder{}
	family(b, "autodoc_server_info", "gauge", "The server, by its version: always 1.")
	sample(b, "autodoc_server_info", labels("version", s.Server.Version), 1)
	family(b, "autodoc_provider_up", "gauge", "Whether the embedding provider's last call succeeded.")
	sample(b, "autodoc_provider_up", "", boolValue(s.Server.Embedding.State == embedder.ProviderUp))

	family(b, "autodoc_http_requests_total", "counter", "The API's requests since the server started, by path and status class.")
	for _, p := range MetricPaths {
		t := totals[p]
		for _, c := range MetricClasses {
			sample(b, "autodoc_http_requests_total", labels("path", p, "class", c), float64(t.ByClass[c]))
		}
	}
	family(b, "autodoc_http_request_duration_seconds", "histogram", "The API's request durations, by path.")
	for _, p := range MetricPaths {
		t := totals[p]
		for i, le := range DurationBuckets {
			var n uint64
			if i < len(t.Buckets) {
				n = t.Buckets[i]
			}
			sample(b, "autodoc_http_request_duration_seconds_bucket", labels("path", p, "le", formatFloat(le)), float64(n))
		}
		sample(b, "autodoc_http_request_duration_seconds_bucket", labels("path", p, "le", "+Inf"), float64(t.Count))
		sample(b, "autodoc_http_request_duration_seconds_sum", labels("path", p), t.Sum.Seconds())
		sample(b, "autodoc_http_request_duration_seconds_count", labels("path", p), float64(t.Count))
	}

	tenants := append([]TenantStatus(nil), s.Tenants...)
	sort.Slice(tenants, func(i, j int) bool { return tenants[i].Tenant < tenants[j].Tenant })
	family(b, "autodoc_tenant_documents", "gauge", "The tenant's documents, in total and ready for semantic search.")
	for _, t := range tenants {
		if t.Error == opcode.None {
			sample(b, "autodoc_tenant_documents", labels("tenant", t.Tenant, "state", "total"), float64(t.Documents.Total))
			sample(b, "autodoc_tenant_documents", labels("tenant", t.Tenant, "state", "ready"), float64(t.Documents.Ready))
		}
	}
	family(b, "autodoc_tenant_quarantined", "gauge", "The tenant's quarantined rows.")
	for _, t := range tenants {
		if t.Error == opcode.None {
			sample(b, "autodoc_tenant_quarantined", labels("tenant", t.Tenant), float64(t.Quarantine))
		}
	}
	family(b, "autodoc_tenant_cursor_lag_seconds", "gauge", "How far the tenant's cursor is behind the database's clock; absent until it first moves.")
	for _, t := range tenants {
		if t.Error == opcode.None && t.Cursor.LagSeconds != nil {
			sample(b, "autodoc_tenant_cursor_lag_seconds", labels("tenant", t.Tenant), float64(*t.Cursor.LagSeconds))
		}
	}
	family(b, "autodoc_tenant_lock_held", "gauge", "Whether this server holds the tenant's writer lock.")
	for _, t := range tenants {
		sample(b, "autodoc_tenant_lock_held", labels("tenant", t.Tenant), boolValue(t.Lock == "held"))
	}
	family(b, "autodoc_tenant_errors", "gauge", "Whether the tenant's last pass, last refresh, or the reading of its state failed: 0 or 1 by kind.")
	for _, t := range tenants {
		sample(b, "autodoc_tenant_errors", labels("tenant", t.Tenant, "kind", "pass"), boolValue(t.LastPass.Error != opcode.None))
		sample(b, "autodoc_tenant_errors", labels("tenant", t.Tenant, "kind", "refresh"), boolValue(t.LastRefresh.Error != opcode.None))
		sample(b, "autodoc_tenant_errors", labels("tenant", t.Tenant, "kind", "tenant"), boolValue(t.Error != opcode.None))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func family(b *strings.Builder, name, kind, help string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, kind)
}

func sample(b *strings.Builder, name, labelSet string, v float64) {
	b.WriteString(name)
	b.WriteString(labelSet)
	b.WriteByte(' ')
	b.WriteString(formatFloat(v))
	b.WriteByte('\n')
}

// labels is a label set from name, value pairs, values escaped as the format requires.
func labels(kv ...string) string {
	if len(kv) == 0 {
		return ""
	}
	b := &strings.Builder{}
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i])
		b.WriteString(`="`)
		b.WriteString(escapeLabel(kv[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// escapeLabel escapes a label value: backslash, double quote and newline.
func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

// escapeHelp escapes a HELP text: backslash and newline.
func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func formatFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
