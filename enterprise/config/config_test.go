package config_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/config"
)

// hashOf is a token's SHA-256 as token_sha256 holds it.
func hashOf(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// good is a complete, valid server.toml.
var good = `
[postgres]
dsn_env = "TEST_SERVER_DSN"
schema  = "autodoc_search"

[embedding]
provider = "openai"
base_url = "https://embeddings.example.internal/v1"
model    = "text-embedding-3-small"
key_env  = "TEST_EMBED_KEY"
slots    = 4

[http]
listen = "127.0.0.1:8730"

[[http.client]]
name         = "web-app"
token_sha256 = "` + hashOf("a long random token, as openssl rand -hex 32 makes") + `"
[http.client.grants.music]
label_id = ["*"]

[[http.client]]
name      = "admin-app"
token_env = "TEST_ADMIN_TOKEN"
[http.client.grants.music]
label_id = ["A", "B"]

[[source]]
tenant   = "music"
view     = "views/track.view"
refresh  = "24h"
interval = "1m"
lag      = "2m"
access   = ["label_id"]
`

// load writes src as server.toml in a test directory, with the environment the good file names,
// and loads it.
func load(t *testing.T, src string) (*config.Server, error) {
	t.Helper()
	t.Setenv("TEST_SERVER_DSN", "postgres://u:secret-password@db.internal/app")
	t.Setenv("TEST_EMBED_KEY", "sk-embedding-secret")
	t.Setenv("TEST_ADMIN_TOKEN", strings.Repeat("x", 64))
	p := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Load(p)
}

// The good file loads with every field as written, its view resolved against the file's directory.
func TestAGoodConfigurationLoads(t *testing.T) {
	s, err := load(t, good)
	if err != nil {
		t.Fatal(err)
	}
	if s.Postgres.DSN() != "postgres://u:secret-password@db.internal/app" || s.Postgres.Schema != "autodoc_search" {
		t.Errorf("Postgres %+v", s.Postgres)
	}
	if s.Embedding != (config.Embedding{Provider: "openai", BaseURL: "https://embeddings.example.internal/v1", Model: "text-embedding-3-small", KeyEnv: "TEST_EMBED_KEY", Slots: 4}) {
		t.Errorf("Embedding %+v", s.Embedding)
	}
	if s.Embedding.Key() != "sk-embedding-secret" || (config.Embedding{}).Key() != "" {
		t.Errorf("the key read from key_env: %q; with none named: %q", s.Embedding.Key(), (config.Embedding{}).Key())
	}
	if s.HTTP == nil || s.HTTP.Listen != "127.0.0.1:8730" || len(s.HTTP.Clients) != 2 ||
		!slices.Equal(s.HTTP.Clients[1].Grants["music"]["label_id"], []string{"A", "B"}) {
		t.Errorf("HTTP %+v", s.HTTP)
	}
	src := s.Sources[0]
	if src.Tenant != "music" || !strings.HasSuffix(src.View, "/views/track.view") || !filepath.IsAbs(src.View) ||
		src.Refresh != 24*time.Hour || src.Interval != time.Minute || src.Lag != 2*time.Minute || !slices.Equal(src.Access, []string{"label_id"}) {
		t.Errorf("Source %+v", src)
	}
	if len(s.Warnings) != 0 {
		t.Errorf("warnings %v", s.Warnings)
	}
}

// What a minimal file leaves out takes its default: the schema, the provider's URL, the slots,
// the durations; and no [http] is no HTTP API.
func TestTheDefaults(t *testing.T) {
	s, err := load(t, `
[postgres]
dsn_env = "TEST_SERVER_DSN"
[embedding]
provider = "ollama"
model = "nomic-embed-text"
[[source]]
tenant = "t"
view = "/abs/t.view"
access = ["org"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Postgres.Schema != config.DefaultSchema || s.Embedding.BaseURL != config.DefaultOllama || s.Embedding.Slots != config.DefaultSlots || s.HTTP != nil {
		t.Errorf("%+v", s)
	}
	if src := s.Sources[0]; src.View != "/abs/t.view" || src.Refresh != config.DefaultRefresh || src.Interval != config.DefaultInterval || src.Lag != config.DefaultLag {
		t.Errorf("source %+v", src)
	}
	s, err = load(t, strings.Replace(good, `base_url = "https://embeddings.example.internal/v1"`, "", 1))
	if err != nil || s.Embedding.BaseURL != config.DefaultOpenAI {
		t.Errorf("openai's default URL: %v %+v", err, s)
	}
}

// The approximate search's settings load as written, and without them are pgstore's defaults; an
// [http] table without listen takes the default address; [metrics] enabled with an operator to read
// it loads enabled, and left out is disabled.
func TestTheSearchAndMetricsSettingsLoad(t *testing.T) {
	t.Setenv("TEST_OPS_TOKEN", strings.Repeat("o", 64))
	s, err := load(t, good)
	if err != nil {
		t.Fatal(err)
	}
	if s.Postgres.ANN != pgstore.DefaultANNConfig() || s.Metrics.Enabled {
		t.Errorf("unset: ANN %+v, metrics %+v; want pgstore's defaults and none", s.Postgres.ANN, s.Metrics)
	}
	src := strings.Replace(good, `schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_threshold = 1\nann_max_scan_tuples = 2147483647\nann_scan_mem_multiplier = 1000\nann_m = 8\nann_ef_construction = 16", 1)
	src = strings.Replace(src, `listen = "127.0.0.1:8730"`, "", 1)
	src = strings.Replace(src, "[[source]]", "[[http.operator]]\nname = \"ops\"\ntoken_env = \"TEST_OPS_TOKEN\"\ntenants = [\"*\"]\n\n[[source]]", 1) + "\n[metrics]\nenabled = true\n"
	if s, err = load(t, src); err != nil {
		t.Fatal(err)
	}
	if want := (pgstore.ANNConfig{Threshold: 1, MaxScanTuples: 2147483647, ScanMemMultiplier: 1000, M: 8, EfConstruction: 16}); s.Postgres.ANN != want {
		t.Errorf("ANN %+v, want %+v", s.Postgres.ANN, want)
	}
	if s.HTTP.Listen != config.DefaultListen || !s.Metrics.Enabled {
		t.Errorf("listen %q (want the default %q), metrics %+v (want enabled)", s.HTTP.Listen, config.DefaultListen, s.Metrics)
	}
}

// Every refusal names its field, and the file is refused whole: a server never starts on a part of
// it. The grant rules are each here: unique names, exactly one of token_sha256 (64 hex digits) and
// token_env (set), every granted tenant a source, every access field of it granted and no other
// field, and a grant of ["*"] alone or of distinct, non-empty values. The mutants, named here: each
// check removed in turn (its row loads).
func TestEveryRefusalNamesItsField(t *testing.T) {
	sub := func(old, new string) string {
		if !strings.Contains(good, old) {
			t.Fatalf("%q is not in the good file", old)
		}
		return strings.Replace(good, old, new, 1)
	}
	lmWebGrant := "[http.client.grants.music]\nlabel_id = [\"*\"]\n\n[[http.client]]\nname      = \"admin-app\""
	for _, c := range []struct {
		name, src, field, says string
	}{
		// postgres
		{"no dsn", sub(`dsn_env = "TEST_SERVER_DSN"`, ""), "postgres.dsn_env", "required"},
		{"dsn unset", sub(`dsn_env = "TEST_SERVER_DSN"`, `dsn_env = "TEST_UNSET_DSN"`), "postgres.dsn_env", "TEST_UNSET_DSN is not set"},
		{"ann threshold 0", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_threshold = 0"), "postgres.ann_threshold", "at least 1 embedding"},
		{"ann scan tuples 0", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_max_scan_tuples = 0"), "postgres.ann_max_scan_tuples", "1 to 2147483647"},
		{"ann scan tuples past int32", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_max_scan_tuples = 2147483648"), "postgres.ann_max_scan_tuples", "1 to 2147483647"},
		{"ann scan memory under 1", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_scan_mem_multiplier = 0.5"), "postgres.ann_scan_mem_multiplier", "1 to 1000"},
		{"ann scan memory over 1000", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_scan_mem_multiplier = 1001"), "postgres.ann_scan_mem_multiplier", "1 to 1000"},
		{"ann m 1", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_m = 1"), "postgres.ann_m", "2 to 100"},
		{"ann m 101", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_m = 101"), "postgres.ann_m", "2 to 100"},
		{"ann ef_construction 1001", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_ef_construction = 1001"), "postgres.ann_ef_construction", "4 to 1000"},
		{"ann ef_construction under twice m", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_m = 40\nann_ef_construction = 79"), "postgres.ann_ef_construction", "at least twice m (40)"},
		{"ann ef_construction under twice the default m", sub(`schema  = "autodoc_search"`, "schema  = \"autodoc_search\"\nann_ef_construction = 31"), "postgres.ann_ef_construction", "at least twice m (16)"},
		{"dsn not a name", sub(`dsn_env = "TEST_SERVER_DSN"`, `dsn_env = "postgres://x"`), "postgres.dsn_env", "is not an environment variable name"},
		{"sealed", sub(`dsn_env = "TEST_SERVER_DSN"`, `dsn_sealed = "AAAA"`), "postgres.dsn_sealed", "not supported yet"},
		{"schema", sub(`schema  = "autodoc_search"`, `schema = "Search-1"`), "postgres.schema", "not a plain schema name"},
		{"plaintext dsn", sub(`schema  = "autodoc_search"`, `dsn = "postgres://u:pw@h/db"`), "postgres.dsn", "unknown field"},
		// embedding
		{"no embedding", sub("[embedding]", "[embeddings]"), "embedding", "required"},
		{"provider", sub(`provider = "openai"`, `provider = "cohere"`), "embedding.provider", `want "openai" or "ollama"`},
		{"no provider", sub(`provider = "openai"`, ``), "embedding.provider", "required"},
		{"base url", sub(`base_url = "https://embeddings.example.internal/v1"`, `base_url = "embeddings.internal"`), "embedding.base_url", "http or https URL"},
		{"model", sub(`model    = "text-embedding-3-small"`, `model = " "`), "embedding.model", "required"},
		{"key unset", sub(`key_env  = "TEST_EMBED_KEY"`, `key_env = "TEST_UNSET_KEY"`), "embedding.key_env", "TEST_UNSET_KEY is not set"},
		{"slots", sub(`slots    = 4`, `slots = 0`), "embedding.slots", "want 1 to 64"},
		{"slots type", sub(`slots    = 4`, `slots = "four"`), "(file)", "slots"},
		// http
		{"listen", sub(`listen = "127.0.0.1:8730"`, `listen = "8730"`), "http.listen", "want host:port"},
		{"off loopback", sub(`listen = "127.0.0.1:8730"`, `listen = "0.0.0.0:8730"`), "http.listen", "needs tls_cert and tls_key"},
		{"half tls", sub(`listen = "127.0.0.1:8730"`, "listen = \"0.0.0.0:8730\"\ntls_cert = \"c.pem\""), "http.tls_cert", "go together"},
		{"tls missing", sub(`listen = "127.0.0.1:8730"`, "listen = \"0.0.0.0:8730\"\ntls_cert = \"c.pem\"\ntls_key = \"k.pem\""), "http.tls_cert", "cannot be read"},
		{"no client", strings.Split(good, "[[http.client]]")[0] + "[[source]]" + strings.SplitN(good, "[[source]]", 2)[1], "http.client", "at least one"},
		{"duplicate name", sub(`name      = "admin-app"`, `name = "web-app"`), "http.client[1] (web-app).name", "names are unique"},
		{"no name", sub(`name      = "admin-app"`, ``), "http.client[1].name", "required"},
		{"both tokens", sub(`token_env = "TEST_ADMIN_TOKEN"`, "token_env = \"TEST_ADMIN_TOKEN\"\ntoken_sha256 = \""+hashOf("x")+"\""), "http.client[1] (admin-app).token_sha256", "not both"},
		{"neither token", sub(`token_env = "TEST_ADMIN_TOKEN"`, ``), "http.client[1] (admin-app).token_sha256", "required"},
		{"a token as its hash", sub(hashOf("a long random token, as openssl rand -hex 32 makes"), "a-plain-bearer-token"), "http.client[0] (web-app).token_sha256", "never the token itself"},
		{"the hash of no token", sub(hashOf("a long random token, as openssl rand -hex 32 makes"), hashOf("")), "http.client[0] (web-app).token_sha256", "no token at all"},
		{"the hash of no token, upper case", sub(hashOf("a long random token, as openssl rand -hex 32 makes"), strings.ToUpper(hashOf(""))), "http.client[0] (web-app).token_sha256", "no token at all"},
		{"a short hash", sub(hashOf("a long random token, as openssl rand -hex 32 makes"), strings.Repeat("ab", 16)), "http.client[0] (web-app).token_sha256", "64 hex digits"},
		{"token unset", sub(`token_env = "TEST_ADMIN_TOKEN"`, `token_env = "TEST_UNSET_TOKEN"`), "http.client[1] (admin-app).token_env", "TEST_UNSET_TOKEN is not set"},
		{"unknown tenant", sub(lmWebGrant, strings.Replace(lmWebGrant, "grants.music", "grants.other-music", 1)), "http.client[0] (web-app).grants.other-music", "not a configured source's tenant"},
		{"missing grant", sub(`label_id = ["A", "B"]`, `region = ["ca"]`), "http.client[1] (admin-app).grants.music.label_id", "every access field of a granted tenant needs a grant"},
		{"a field not an access field", sub(`label_id = ["A", "B"]`, "label_id = [\"A\"]\nregion = [\"ca\"]"), "http.client[1] (admin-app).grants.music.region", "not one of the tenant's access fields"},
		{"star and a value", sub(`label_id = ["A", "B"]`, `label_id = ["*", "A"]`), "http.client[1] (admin-app).grants.music.label_id", `"*" stands alone`},
		{"empty grant", sub(`label_id = ["A", "B"]`, `label_id = []`), "http.client[1] (admin-app).grants.music.label_id", "empty"},
		{"empty value", sub(`label_id = ["A", "B"]`, `label_id = ["A", " "]`), "http.client[1] (admin-app).grants.music.label_id", "an empty value"},
		{"repeated value", sub(`label_id = ["A", "B"]`, `label_id = ["A", "A"]`), "http.client[1] (admin-app).grants.music.label_id", "granted twice"},
		{"no grants", sub("[http.client.grants.music]\nlabel_id = [\"A\", \"B\"]", ""), "http.client[1] (admin-app).grants", "required"},
		// sources
		{"no source", strings.SplitN(good, "[[source]]", 2)[0], "source", "at least one"},
		{"no tenant", sub(`tenant   = "music"`, ``), "source[0].tenant", "required"},
		{"bad tenant", sub(`tenant   = "music"`, `tenant = "lm catalog"`), "source[0].tenant", "letters, digits"},
		{"duplicate tenant", good + "[[source]]\ntenant = \"music\"\nview = \"v.view\"\naccess = [\"x\"]\n", "source[1].tenant", "a tenant of its own"},
		{"no view", sub(`view     = "views/track.view"`, ``), "source[0].view", "required"},
		{"interval", sub(`interval = "1m"`, `interval = "10ms"`), "source[0].interval", "want at least 1s"},
		{"refresh", sub(`refresh  = "24h"`, `refresh = "30s"`), "source[0].refresh", "at least the interval"},
		{"lag", sub(`lag      = "2m"`, `lag = "-1m"`), "source[0].lag", "zero or more"},
		{"not a duration", sub(`lag      = "2m"`, `lag = "soon"`), "(file)", "want a duration"},
		{"no access", sub(`access   = ["label_id"]`, ``), "source[0].access", "required"},
		{"access twice", sub(`access   = ["label_id"]`, `access = ["label_id", "label_id"]`), "source[0].access", "named twice"},
		{"access with =", sub(`access   = ["label_id"]`, `access = ["label=id"]`), "source[0].access", "not an access field name"},
		{"metrics with no operator", good + "\n[metrics]\nenabled = true\n", "metrics.enabled", "no [[http.operator]]"},
		{"metrics with no http", good[:strings.Index(good, "[http]")] + good[strings.Index(good, "[[source]]"):] + "\n[metrics]\nenabled = true\n", "metrics.enabled", "no [http] table"},
		{"unknown", good + "\n[telemetry]\nport = 9\n", "telemetry", "unknown field"}, // [metrics] is a known table
		{"not toml", good + "\n[[source]\n", "(file)", "line"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, c.src)
			var r *config.Refusal
			if !errors.As(err, &r) || !config.IsRefusal(err) {
				t.Fatalf("Load: %v, want a refusal", err)
			}
			i := slices.Index(r.Fields(), c.field)
			if i < 0 || !strings.Contains(r.Errors[i].Reason, c.says) {
				t.Fatalf("refused %v (%v), want %s saying %q", r.Fields(), err, c.field, c.says)
			}
			for _, secret := range []string{"secret-password", "sk-embedding-secret", strings.Repeat("x", 64), "a-plain-bearer-token", "u:pw@h"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the refusal shows a secret: %v", err)
				}
			}
		})
	}
	if _, err := config.Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil || config.IsRefusal(err) {
		t.Errorf("a missing file: %v, want an error that is not a refusal", err)
	}
}

// A token read from the environment shorter than 32 bytes is allowed, and warned of; TLS files
// that exist admit a listener beyond loopback; a refusal of one field reads as the file's.
func TestWarningsAndTLS(t *testing.T) {
	t.Setenv("TEST_SHORT", "short")
	s, err := load(t, strings.Replace(good, `token_env = "TEST_ADMIN_TOKEN"`, `token_env = "TEST_SHORT"`, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "shorter than 32 bytes") || strings.Contains(s.Warnings[0], "short\"") {
		t.Errorf("warnings %v", s.Warnings)
	}
	dir := t.TempDir()
	for _, f := range []string{"c.pem", "k.pem"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tls := strings.Replace(good, `listen = "127.0.0.1:8730"`, "listen = \"0.0.0.0:8730\"\ntls_cert = \""+dir+"/c.pem\"\ntls_key = \""+dir+"/k.pem\"", 1)
	if s, err := load(t, tls); err != nil || s.HTTP.TLSKey != dir+"/k.pem" {
		t.Errorf("TLS off loopback: %v", err)
	}
	if s, err := load(t, strings.Replace(good, `listen = "127.0.0.1:8730"`, `listen = "localhost:8730"`, 1)); err != nil || s.HTTP.Listen != "localhost:8730" {
		t.Errorf("localhost: %v", err)
	}
	r := config.Refuse("server.toml", "source[0].view", "args: want exactly")
	if r.Error() != "server.toml: source[0].view: args: want exactly" || !config.IsRefusal(r) {
		t.Errorf("Refuse: %v", r)
	}
}
