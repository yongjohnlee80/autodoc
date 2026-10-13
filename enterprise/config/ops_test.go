package config_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
)

// operator is an [[http.operator]] block, inserted before the good file's [[source]].
func withOperator(block string) string {
	return strings.Replace(good, "[[source]]", block+"\n[[source]]", 1)
}

// An operator loads with its token and tenants; ["*"] names every tenant. The defaults: no proxy,
// no operator, the ingester's own refresh retry, metrics every minute.
func TestAnOperatorAndTheOperationsFieldsLoad(t *testing.T) {
	t.Setenv("TEST_OPS_TOKEN", strings.Repeat("o", 64))
	s, err := load(t, withOperator("[[http.operator]]\nname = \"ops\"\ntoken_env = \"TEST_OPS_TOKEN\"\ntenants = [\"lm-catalog\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.HTTP.Operators) != 1 || s.HTTP.Operators[0].Name != "ops" || !slices.Equal(s.HTTP.Operators[0].Tenants, []string{"lm-catalog"}) {
		t.Errorf("operators %+v", s.HTTP.Operators)
	}
	if s.HTTP.Proxy || s.Sources[0].RefreshRetry != 0 || s.Log.MetricsInterval != config.DefaultMetricsInterval {
		t.Errorf("defaults: proxy %v, refresh_retry %v, metrics %v", s.HTTP.Proxy, s.Sources[0].RefreshRetry, s.Log.MetricsInterval)
	}
	s, err = load(t, strings.Replace(withOperator("[[http.operator]]\nname = \"ops\"\ntoken_sha256 = \""+hashOf("an operator's long random token")+"\"\ntenants = [\"*\"]\n"),
		`lag      = "2m"`, "lag      = \"2m\"\nrefresh_retry = \"30m\"", 1)+"\n[log]\nmetrics_interval = \"0s\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.HTTP.Operators[0].Tenants, []string{"*"}) || s.Sources[0].RefreshRetry != 30*time.Minute || s.Log.MetricsInterval != 0 {
		t.Errorf("operator %+v, refresh_retry %v, metrics %v", s.HTTP.Operators[0], s.Sources[0].RefreshRetry, s.Log.MetricsInterval)
	}
}

// Each refusal of the operations fields names its field, and never echoes a token: an operator
// without a name, with a client's name, with both or neither token form, with no tenants, an
// unknown tenant, a tenant twice or "*" with others; the same token as a client's (one token, one
// role); proxy with TLS; refresh_retry below a minute or above the refresh; metrics too often.
func TestEveryOperationsRefusalNamesItsField(t *testing.T) {
	t.Setenv("TEST_OPS_TOKEN", strings.Repeat("o", 64))
	op := func(body string) string { return withOperator("[[http.operator]]\n" + body + "\n") }
	adminToken := strings.Repeat("x", 64) // the good file's lm-admin token, through TEST_ADMIN_TOKEN
	for _, c := range []struct {
		name, src, field, says string
	}{
		{"no name", op(`token_env = "TEST_OPS_TOKEN"` + "\n" + `tenants = ["*"]`), "http.operator[0].name", "required"},
		{"a client's name", op("name = \"lm-web\"\ntoken_env = \"TEST_OPS_TOKEN\"\ntenants = [\"*\"]"), "http.operator[0] (lm-web).name", "unique across clients and operators"},
		{"both tokens", op("name = \"ops\"\ntoken_env = \"TEST_OPS_TOKEN\"\ntoken_sha256 = \"" + hashOf("x") + "\"\ntenants = [\"*\"]"), "http.operator[0] (ops).token_sha256", "not both"},
		{"no token", op("name = \"ops\"\ntenants = [\"*\"]"), "http.operator[0] (ops).token_sha256", "required"},
		{"no tenants", op("name = \"ops\"\ntoken_env = \"TEST_OPS_TOKEN\""), "http.operator[0] (ops).tenants", "required"},
		{"unknown tenant", op("name = \"ops\"\ntoken_env = \"TEST_OPS_TOKEN\"\ntenants = [\"nobody\"]"), "http.operator[0] (ops).tenants", "not a configured source's tenant"},
		{"a tenant twice", op("name = \"ops\"\ntoken_env = \"TEST_OPS_TOKEN\"\ntenants = [\"lm-catalog\", \"lm-catalog\"]"), "http.operator[0] (ops).tenants", "named twice"},
		{"* with others", op("name = \"ops\"\ntoken_env = \"TEST_OPS_TOKEN\"\ntenants = [\"*\", \"lm-catalog\"]"), "http.operator[0] (ops).tenants", "stands alone"},
		{"a client's token by hash", op("name = \"ops\"\ntoken_sha256 = \"" + hashOf(adminToken) + "\"\ntenants = [\"*\"]"), "http.operator[0] (ops).token_sha256", "http.client[1] holds the same token"},
		{"a client's token by env", op("name = \"ops\"\ntoken_env = \"TEST_ADMIN_TOKEN\"\ntenants = [\"*\"]"), "http.operator[0] (ops).token_sha256", "one token, one role"},
		{"proxy with tls", strings.Replace(good, `listen = "127.0.0.1:8730"`, "listen = \"0.0.0.0:8730\"\nproxy = true\ntls_cert = \"c.pem\"\ntls_key = \"k.pem\"", 1), "http.proxy", "contradict"},
		{"retry under a minute", strings.Replace(good, `lag      = "2m"`, "lag      = \"2m\"\nrefresh_retry = \"30s\"", 1), "source[0].refresh_retry", "want 1m0s up to the refresh"},
		{"retry over the refresh", strings.Replace(good, `lag      = "2m"`, "lag      = \"2m\"\nrefresh_retry = \"48h\"", 1), "source[0].refresh_retry", "want 1m0s up to the refresh"},
		{"metrics too often", good + "\n[log]\nmetrics_interval = \"5s\"\n", "log.metrics_interval", "want 0 (none) or at least 10s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, c.src)
			var r *config.Refusal
			if !errorsAs(err, &r) {
				t.Fatalf("not refused: %v", err)
			}
			found := false
			for _, e := range r.Errors {
				if e.Field == c.field && strings.Contains(e.Reason, c.says) {
					found = true
				}
			}
			if !found {
				t.Errorf("refusals %v, want %s: …%s…", err, c.field, c.says)
			}
			if strings.Contains(err.Error(), adminToken) || strings.Contains(err.Error(), strings.Repeat("o", 64)) {
				t.Errorf("a token is in the refusal: %v", err)
			}
		})
	}
}

// proxy = true lets the API listen off loopback without TLS, with a --check warning that the
// requests cross the network in clear text; on loopback it warns of nothing.
func TestProxyListensOffLoopbackWithAWarning(t *testing.T) {
	s, err := load(t, strings.Replace(good, `listen = "127.0.0.1:8730"`, "listen = \"0.0.0.0:8730\"\nproxy = true", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !s.HTTP.Proxy || len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "clear text") {
		t.Errorf("proxy %v, warnings %v", s.HTTP.Proxy, s.Warnings)
	}
	if s, err := load(t, strings.Replace(good, `listen = "127.0.0.1:8730"`, "listen = \"127.0.0.1:8730\"\nproxy = true", 1)); err != nil || len(s.Warnings) != 0 {
		t.Errorf("proxy on loopback: %v, %v", err, s.Warnings)
	}
}

func errorsAs(err error, r **config.Refusal) bool {
	if err == nil {
		return false
	}
	var ok bool
	*r, ok = err.(*config.Refusal)
	return ok
}
