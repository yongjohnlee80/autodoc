package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// TestAKeyStaysWithItsServer: an edit that leaves the key out keeps it only while the base URL
// stays on the stored server. Moving a keyed provider or ranker to another server without the key
// typed again is refused with nothing changed; typed again, or removed, it is kept; a path on the
// same server, or a provider with no key, moves freely.
func TestAKeyStaysWithItsServer(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
	if _, err := s.AddProvider(ctx, ProviderSpec{Name: "p", Kind: KindOpenAI, BaseURL: "https://api.example/v1", Model: "m", Key: strp("sk-p")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRanker(ctx, RankerSpec{Name: "r", Kind: KindTEI, BaseURL: "https://rank.example", Key: strp("sk-r")}); err != nil {
		t.Fatal(err)
	}

	err := s.UpdateProvider(ctx, "p", ProviderSpec{Name: "p", Kind: KindOpenAI, BaseURL: "https://attacker.example/v1", Model: "m"})
	if !errors.Is(err, ErrKeyForNewServer) {
		t.Fatalf("a provider moved to another server with its key left out: %v, want ErrKeyForNewServer", err)
	}
	if info, key, _ := s.ProviderWithKey(ctx, "p"); info.BaseURL != "https://api.example/v1" || key != "sk-p" {
		t.Fatalf("after the refused move: %q, key %q; want it unchanged", info.BaseURL, key)
	}
	err = s.UpdateRanker(ctx, "r", RankerSpec{Name: "r", Kind: KindTEI, BaseURL: "http://rank.example"})
	if !errors.Is(err, ErrKeyForNewServer) {
		t.Fatalf("a ranker moved to http on the same host: %v, want ErrKeyForNewServer", err)
	}
	if info, key, _ := s.RankerWithKey(ctx, "r"); info.BaseURL != "https://rank.example" || key != "sk-r" {
		t.Fatalf("after the refused move: %q, key %q; want it unchanged", info.BaseURL, key)
	}

	// the same server under another path or the default port written keeps the key
	if err := s.UpdateProvider(ctx, "p", ProviderSpec{Name: "p", Kind: KindOpenAI, BaseURL: "https://API.example:443/v2", Model: "m"}); err != nil {
		t.Fatalf("a path on the same server: %v", err)
	}
	if _, key, _ := s.ProviderWithKey(ctx, "p"); key != "sk-p" {
		t.Fatalf("a path on the same server lost the key: %q", key)
	}
	// typed again, the key moves with the edit
	if err := s.UpdateProvider(ctx, "p", ProviderSpec{Name: "p", Kind: KindOpenAI, BaseURL: "https://other.example", Model: "m", Key: strp("sk-new")}); err != nil {
		t.Fatalf("a move with the key typed: %v", err)
	}
	if _, key, _ := s.ProviderWithKey(ctx, "p"); key != "sk-new" {
		t.Fatalf("the typed key: %q", key)
	}
	// removed, nothing is left to send
	if err := s.UpdateRanker(ctx, "r", RankerSpec{Name: "r", Kind: KindTEI, BaseURL: "https://elsewhere.example", Key: strp("")}); err != nil {
		t.Fatalf("a move with the key removed: %v", err)
	}
	// with no key stored, a move needs none
	if err := s.UpdateRanker(ctx, "r", RankerSpec{Name: "r", Kind: KindTEI, BaseURL: "https://third.example"}); err != nil {
		t.Fatalf("a keyless ranker moved: %v", err)
	}
}

// TestLendKey: the rule the daemon applies before it calls an edit's endpoint with a stored key.
func TestLendKey(t *testing.T) {
	typed := "sk-typed"
	for _, c := range []struct {
		name, stored, edit, key string
		typed                   *string
		err                     error
	}{
		{"the same server", "https://a.example/v1", "https://a.example/v1/", "sk", nil, nil},
		{"the default port written", "http://a.example", "http://a.example:80/x", "sk", nil, nil},
		{"a host in other case", "https://A.example", "https://a.example", "sk", nil, nil},
		{"another host", "https://a.example", "https://b.example", "", nil, ErrKeyForNewServer},
		{"another port", "http://127.0.0.1:8080", "http://127.0.0.1:8081", "", nil, ErrKeyForNewServer},
		{"another scheme", "https://a.example", "http://a.example", "", nil, ErrKeyForNewServer},
		{"a userinfo trick", "https://a.example", "https://a.example@b.example", "", nil, ErrKeyForNewServer},
		{"an unparsable edit", "https://a.example", "http://[::1", "", nil, ErrKeyForNewServer},
		{"a key typed", "https://a.example", "https://b.example", "sk-typed", &typed, nil},
	} {
		key, err := LendKey(c.stored, "sk", c.edit, c.typed)
		if !errors.Is(err, c.err) || key != c.key {
			t.Errorf("%s: %q, %v; want %q, %v", c.name, key, err, c.key, c.err)
		}
	}
	if key, err := LendKey("https://a.example", "", "https://b.example", nil); err != nil || key != "" {
		t.Errorf("no key stored: %q, %v; want none and no refusal", key, err)
	}
}
