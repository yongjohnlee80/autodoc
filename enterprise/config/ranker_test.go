package config_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
)

// [ranker] is optional: absent, there is none; present, its kind's client, with the window,
// timeout and slots defaulted and held to their bounds, and a key from the environment.
func TestTheRankerSection(t *testing.T) {
	s, err := load(t, good)
	if err != nil {
		t.Fatal(err)
	}
	if s.Ranker != nil {
		t.Fatalf("no [ranker]: %+v, want none", s.Ranker)
	}
	t.Setenv("TEST_RANK_KEY", "rk-ranker-secret")
	s, err = load(t, good+"\n[ranker]\nkind = \"rerank-api\"\nbase_url = \"https://rerank.example.internal\"\nmodel = \"rerank-v3.5\"\nkey_env = \"TEST_RANK_KEY\"\n")
	if err != nil {
		t.Fatal(err)
	}
	want := config.Ranker{Kind: "rerank-api", BaseURL: "https://rerank.example.internal", Model: "rerank-v3.5", KeyEnv: "TEST_RANK_KEY",
		Window: 40, Timeout: 750 * time.Millisecond, Slots: 4}
	if s.Ranker == nil || *s.Ranker != want || s.Ranker.Key() != "rk-ranker-secret" {
		t.Fatalf("ranker %+v, want %+v", s.Ranker, want)
	}
	s, err = load(t, good+"\n[ranker]\nkind = \"tei\"\nbase_url = \"http://127.0.0.1:8081\"\nwindow = 100\ntimeout = \"2s\"\nslots = 8\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := (config.Ranker{Kind: "tei", BaseURL: "http://127.0.0.1:8081", Window: 100, Timeout: 2 * time.Second, Slots: 8}); *s.Ranker != want {
		t.Fatalf("ranker %+v, want %+v", s.Ranker, want)
	}
}

// Each [ranker] refusal names its field, and none shows the key.
func TestEveryRankerRefusalNamesItsField(t *testing.T) {
	t.Setenv("TEST_RANK_KEY", "rk-ranker-secret")
	tei := "\n[ranker]\nkind = \"tei\"\nbase_url = \"http://127.0.0.1:8081\"\n"
	for _, c := range []struct{ name, add, field, says string }{
		{"no kind", "\n[ranker]\nbase_url = \"http://127.0.0.1:8081\"\n", "ranker.kind", "required"},
		{"a kind it lacks", "\n[ranker]\nkind = \"ollama\"\nbase_url = \"http://127.0.0.1:8081\"\n", "ranker.kind", `want "tei", "rerank-api" or "vertex"`},
		{"no base url", "\n[ranker]\nkind = \"tei\"\n", "ranker.base_url", "required"},
		{"a base url without a scheme", "\n[ranker]\nkind = \"tei\"\nbase_url = \"rerank.internal\"\n", "ranker.base_url", "http or https URL"},
		{"tei with a model", tei + "model = \"bge\"\n", "ranker.model", "leave model out"},
		{"rerank-api without a model", "\n[ranker]\nkind = \"rerank-api\"\nbase_url = \"https://r.example\"\n", "ranker.model", "required for rerank-api"},
		{"a key unset", tei + "key_env = \"TEST_UNSET_RANK_KEY\"\n", "ranker.key_env", "is not set"},
		{"a window too small", tei + "window = 9\n", "ranker.window", "want 10 to 100"},
		{"a window too large", tei + "window = 101\n", "ranker.window", "want 10 to 100"},
		{"a timeout too short", tei + "timeout = \"99ms\"\n", "ranker.timeout", "want 100ms to 10s"},
		{"a timeout too long", tei + "timeout = \"11s\"\n", "ranker.timeout", "want 100ms to 10s"},
		{"no slots", tei + "slots = 0\n", "ranker.slots", "want 1 to 64"},
		{"an unknown field", tei + "threshold = 0.5\n", "ranker.threshold", "unknown field"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := load(t, good+c.add)
			var r *config.Refusal
			if !errors.As(err, &r) {
				t.Fatalf("Load: %v, want a refusal", err)
			}
			i := slices.Index(r.Fields(), c.field)
			if i < 0 || !strings.Contains(r.Errors[i].Reason, c.says) {
				t.Fatalf("refused %v (%v), want %s saying %q", r.Fields(), err, c.field, c.says)
			}
			if strings.Contains(err.Error(), "rk-ranker-secret") {
				t.Errorf("the refusal shows the key: %v", err)
			}
		})
	}
}
