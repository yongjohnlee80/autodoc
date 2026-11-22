package searchsetup

import (
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// A list read before a removal and answering after the list the removal read again is dropped,
// for both lists: b, removed, is not listed again, and the one in use is the later list's. The
// mutant, named here: load's ticket with no sequence (b back, and a in use again).
func TestAnEarlierListAnsweringLastIsDropped(t *testing.T) {
	for _, tc := range endpointCases {
		t.Run(tc.name, func(t *testing.T) {
			c := newScripted("kb")
			e := tc.make(c)
			scriptedList(c, e)
			c.Deferred = true
			e.load() // read with a and b, a in use
			k0 := e.spec.kinds[0]
			c.Answers[e.spec.list] = func([]any) (any, error) { // a removed meanwhile, b in use
				return map[string]any{e.spec.noun + "s": []any{
					map[string]any{"name": "b", "kind": k0.kind, "base_url": k0.base, "model": "m0"},
				}, "active": "b"}, nil
			}
			e.load()
			c.Deliver(1, 0) // the later list first
			var names []string
			for _, r := range e.Rows.All() {
				names = append(names, r.Name)
			}
			if !slices.Equal(names, []string{"b"}) || e.Active != "b" {
				t.Fatalf("listed %v, in use %q once the earlier list answered last; want [b], b", names, e.Active)
			}
		})
	}
}

// A Check of a base URL since edited, answering after the Check of the new one, is dropped, for
// both lists: the form says what the base URL in it serves. The mutant, named here: check's ticket
// with no sequence (the old URL's models offered).
func TestAnEarlierChecksModelsAreDropped(t *testing.T) {
	for _, tc := range endpointCases {
		t.Run(tc.name, func(t *testing.T) {
			c := newScripted("kb")
			e := tc.make(c)
			var got []string
			e.spec.checked = func(models []string, err error) { got = models }
			c.Answers[e.spec.verb+".models"] = func(p []any) (any, error) {
				if feature.Str(feature.AsMap(p[len(p)-1]), "base_url") == "http://first:1" {
					return []any{"old"}, nil
				}
				return []any{"new"}, nil
			}
			c.Deferred = true
			e.check("http://first:1", "k")
			e.check("http://second:2", "k")
			c.Deliver(1, 0)
			if !slices.Equal(got, []string{"new"}) {
				t.Fatalf("the form was given %v once the earlier Check answered last; want [new], the second URL's", got)
			}
		})
	}
}

// A model's context maximum asked of one server, then of another with the same model name (the
// base URL edited, or the kind changed), answering last, is dropped: the hint is the server in the
// form's. Asked, then the kind chosen anew with no window and nothing asked, the answer is dropped
// too: no maximum, hint or window for a kind sent none. The mutants, named here: the guard on the
// model name alone (the first server's maximum); maxAsked kept when the kind changes unasked.
func TestAnEarlierContextMaximumIsDropped(t *testing.T) {
	t.Run("kind with no window", func(t *testing.T) {
		c := newScripted("kb")
		p := newProviders(c)
		c.Answers["embedding.model_context"] = func([]any) (any, error) { return map[string]any{"maximum": int64(2048)}, nil }
		c.Deferred = true
		p.kind = 0
		p.checkModelContext("http://first:1", "k", "m")
		p.kindChosen(2, "http://first:1") // OpenAI-compatible: sent no window
		hint, window := c.Sets["App.providerContextHint"], c.Sets["App.providerContext"]
		c.Deliver(0)
		if p.max != 0 || c.Sets["App.providerContextHint"] != hint || c.Sets["App.providerContext"] != window {
			t.Fatalf("the earlier answer applied to a kind with no window: max %d, hint %v (was %v), window %v (was %v)",
				p.max, c.Sets["App.providerContextHint"], hint, c.Sets["App.providerContext"], window)
		}
	})
	for _, change := range []string{"base URL", "kind"} {
		t.Run(change, func(t *testing.T) {
			c := newScripted("kb")
			p := newProviders(c)
			c.Answers["embedding.model_context"] = func(a []any) (any, error) {
				spec := feature.AsMap(a[1])
				if feature.Str(spec, "base_url") == "http://first:1" && feature.Str(spec, "kind") == providerKinds[0].kind {
					return map[string]any{"maximum": int64(2048)}, nil
				}
				return map[string]any{"maximum": int64(8192)}, nil
			}
			c.Deferred = true
			p.kind = 0
			p.checkModelContext("http://first:1", "k", "m")
			if change == "kind" {
				p.kind = 1 // Ollama Cloud: a window too, the same model name
				p.checkModelContext("http://first:1", "k", "m")
			} else {
				p.checkModelContext("http://second:2", "k", "m")
			}
			c.Deliver(1, 0)
			if p.max != 8192 {
				t.Fatalf("the maximum is %d once the earlier ask answered last; want 8192, the %s changed", p.max, change)
			}
		})
	}
}
