package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// endpointCase is one of the AI models' lists, built over a scripted core.
type endpointCase struct {
	name string
	make func(c Core) *endpoints
}

var endpointCases = []endpointCase{
	{"providers", func(c Core) *endpoints { return newProviders(c).endpoints }},
	{"rankers", func(c Core) *endpoints { return newRankers(c, func() {}).endpoints }},
}

// scriptedList answers e's list verb: a, of the list's second kind (one that takes a key, its key
// sealed) and in use, and b, of its first.
func scriptedList(c *scriptedCore, e *endpoints) {
	k0, k1 := e.spec.kinds[0], e.spec.kinds[1]
	c.answers[e.spec.list] = func([]any) (any, error) {
		return map[string]any{e.spec.noun + "s": []any{
			map[string]any{"name": "a", "kind": k1.kind, "base_url": k1.base, "model": "m1", "has_key": true},
			map[string]any{"name": "b", "kind": k0.kind, "base_url": k0.base, "model": "m0"},
		}, "active": "a"}, nil
	}
}

func (s *scriptedCore) saw(view string) bool { return slices.Contains(s.views, view) }

// Every step the providers and the rankers share runs once, in the endpoints core, the same for
// both: the list and the one in use, a choice and a refused one, a removal asked first, the form on
// Add… and on Edit… (the key kept), the kind chooser, Check lending the stored key, and a save,
// refused and not. Each step passing for both lists is what makes the core theirs, not a copy.
func TestTheEndpointsCoreServesBothLists(t *testing.T) {
	for _, tc := range endpointCases {
		t.Run(tc.name, func(t *testing.T) {
			c := newScripted("kb")
			e := tc.make(c)
			scriptedList(c, e)
			noun, changed := e.spec.noun, "search.changed ranker="+map[bool]string{true: "true", false: "false"}[e.spec.ranker]

			e.load()
			if e.rows.Len() != 2 || e.active != "a" || !strings.Contains(c.set[e.src("sStatus")].(string), "a") {
				t.Fatalf("listed %d, in use %q, status %q", e.rows.Len(), e.active, c.set[e.src("sStatus")])
			}
			if row := e.rowOf(0, e.rows.All()[0]); row["use"] != "●" || row["apiKey"] != "sealed" || row["kind"] != e.spec.kinds[1].label {
				t.Fatalf("a's row %v", row)
			}
			if !c.called(e.spec.verb + ".usage[a") {
				t.Fatal("the row under the cursor's usage was not read")
			}

			// a choice refused leaves the one in use; one taken lists again and tells the search
			c.answers[e.spec.verb+".use"] = func([]any) (any, error) { return nil, errors.New("no") }
			var refused, used bool
			e.choose("b", func(error) { refused = true }, func() { used = true })
			if !refused || used || c.saw(changed) {
				t.Fatalf("a refused choice: refused %v, used %v, search told %v", refused, used, c.saw(changed))
			}
			c.answers[e.spec.verb+".use"] = func([]any) (any, error) { return nil, nil }
			e.choose("b", func(error) { refused = true }, func() { used = true })
			if !used || !c.saw(changed) {
				t.Fatalf("a choice taken: used %v, search told %v", used, c.saw(changed))
			}

			// a removal asks first, and Yes removes the row asked about
			c.views = nil
			e.startRemove(1)
			if len(c.asked) != 1 || !strings.Contains(c.asked[0].Text, "Remove the "+noun+" b?") {
				t.Fatalf("asked %+v", c.asked)
			}
			e.cursor = 0 // the cursor moving while the question is open changes nothing
			c.yes()
			if !c.called(e.spec.verb+".remove[b]") || !slices.Contains(c.notes, "removed the "+noun+" b") || !c.saw(changed) {
				t.Fatalf("Yes: calls %v, notes %v, views %v", c.calls, c.notes, c.views)
			}

			// the form on Add…: the spec's blank, its key label, and the form's dialog
			e.startAdd()
			if c.set[e.src("FormTitle")] != e.spec.addTitle || c.set[e.src("Name")] != "" || c.opened[len(c.opened)-1] != noun+"Edit" {
				t.Fatalf("Add…: title %q, name %q, opened %v", c.set[e.src("FormTitle")], c.set[e.src("Name")], c.opened)
			}
			// on Edit…, a's key sealed: kept unless one is typed
			e.startEdit(0)
			if c.set[e.src("Name")] != "a" || c.set[e.src("KeyLabel")] != "API key (sealed; leave empty to keep it)" {
				t.Fatalf("Edit… a: name %q, key label %q", c.set[e.src("Name")], c.set[e.src("KeyLabel")])
			}
			e.startEdit(1) // b has no key sealed: nothing to keep
			if c.set[e.src("KeyLabel")] == "API key (sealed; leave empty to keep it)" {
				t.Fatal("Edit… b, with no key, says its key is sealed")
			}
			e.startEdit(0)
			// the kind chooser swaps a kind's default base URL, and keeps one typed
			e.kindChosen(0, e.spec.kinds[1].base)
			if c.set[e.src("Base")] != e.spec.kinds[0].base {
				t.Fatalf("the default base URL was not swapped: %q", c.set[e.src("Base")])
			}
			e.kindChosen(1, "http://mine:9000")
			if c.set[e.src("Base")] == e.spec.kinds[1].base {
				t.Fatal("a typed base URL was replaced by the kind's default")
			}
			// Check, no key typed: the edited entry lends its stored one
			e.check("http://mine:9000", "")
			if !c.called(e.spec.verb + ".models[a map[") {
				t.Fatalf("Check asked %v", c.calls)
			}

			// a save refused opens the form again, saying why; one taken tells the search
			c.answers[e.spec.verb+".update"] = func([]any) (any, error) { return nil, errors.New("bad url") }
			c.opened, c.views = nil, nil
			e.save("a", "http://mine:9000", "m", "", "")
			if !strings.Contains(c.set[e.src("FormError")].(string), "not saved: bad url") || !slices.Equal(c.opened, []string{noun + "Edit"}) || c.saw(changed) {
				t.Fatalf("a refused save: error %q, opened %v, views %v", c.set[e.src("FormError")], c.opened, c.views)
			}
			c.answers[e.spec.verb+".update"] = func([]any) (any, error) { return nil, nil }
			e.save("a", "http://mine:9000", "m", "", "")
			if !slices.Contains(c.notes, "saved the "+noun+" a") || !c.saw(changed) {
				t.Fatalf("a save: notes %v, views %v", c.notes, c.views)
			}
		})
	}
}

// A kind that takes no key keeps none: a save sends an empty one, whatever is typed; a kind that
// takes one sends the one typed, and none when none is (the stored one is kept).
func TestAKeylessKindKeepsNoKey(t *testing.T) {
	c := newScripted("kb")
	p := newProviders(c)
	var sent []map[string]any
	c.answers["embedding.add"] = func(params []any) (any, error) { sent = append(sent, params[0].(map[string]any)); return nil, nil }
	p.startAdd() // a local Ollama: no key
	p.save("local", "http://127.0.0.1:11434", "nomic", "typed", "")
	p.kindChosen(1, "") // Ollama Cloud: a key
	p.save("cloud", "https://ollama.com", "nomic", "", "")
	p.save("cloud", "https://ollama.com", "nomic", "sk", "")
	key := func(i int) (any, bool) { v, ok := sent[i]["key"]; return v, ok }
	if v, ok := key(0); !ok || v != "" {
		t.Errorf("a local Ollama sent key %v (%v), want an empty one", v, ok)
	}
	if _, ok := key(1); ok {
		t.Error("a cloud provider with no key typed sent one")
	}
	if v, _ := key(2); v != "sk" {
		t.Errorf("a cloud provider sent key %v, want the one typed", v)
	}
}

// A provider's context window is a number of tokens, at most its model's maximum when it is known.
func TestAProvidersWindowIsBounded(t *testing.T) {
	c := newScripted("kb")
	p := newProviders(c)
	p.startAdd()
	p.save("local", "http://127.0.0.1:11434", "nomic", "", "lots")
	if !strings.Contains(c.set["App.providerFormError"].(string), "a number of tokens") || c.called("embedding.add") {
		t.Fatalf("a window of lots: error %q, calls %v", c.set["App.providerFormError"], c.calls)
	}
	p.max = 4096
	p.save("local", "http://127.0.0.1:11434", "nomic", "", "8192")
	if !strings.Contains(c.set["App.providerFormError"].(string), "maximum of 4096") || c.called("embedding.add") {
		t.Fatalf("a window over the maximum: error %q, calls %v", c.set["App.providerFormError"], c.calls)
	}
}

// A build that supplies its own ranker shows it, and none can be chosen: Use, Don't use and
// Window… do nothing, and the dialog's buttons say so on the rankers' tab.
func TestASuppliedRankerCannotBeChosen(t *testing.T) {
	c := newScripted("kb")
	a := newAIModels(c)
	c.answers["ranker.list"] = func([]any) (any, error) {
		return map[string]any{"rankers": []any{map[string]any{"name": "r", "kind": "tei"}}, "supplied": "build-ranker", "window": int64(10)}, nil
	}
	a.tabMoved(rankerTab)
	if a.rankers.supplied != "build-ranker" || c.set["App.aiRankerChoice"] != false || c.set["App.aiUseEnabled"] != false {
		t.Fatalf("supplied %q, choice %v, Use %v", a.rankers.supplied, c.set["App.aiRankerChoice"], c.set["App.aiUseEnabled"])
	}
	c.calls = nil
	a.use(0)
	a.rankers.stop()
	a.rankers.startWindow()
	if len(c.calls) != 0 {
		t.Fatalf("a supplied ranker was chosen or windowed: %v", c.calls)
	}
}

// The buttons both tabs share act on the tab showing: on the providers' the row the dialog passes,
// on the rankers' the row under the rankers' cursor.
func TestTheSharedButtonsActOnTheTabShowing(t *testing.T) {
	c := newScripted("kb")
	a := newAIModels(c)
	scriptedList(c, a.providers.endpoints)
	scriptedList(c, a.rankers.endpoints)
	a.providers.load()
	a.rankers.load()
	a.edit(1)
	if a.providers.editing != "b" {
		t.Fatalf("Edit… on the providers' tab edits %q, want the row passed, b", a.providers.editing)
	}
	a.tabMoved(rankerTab)
	a.rankers.cursor = 0
	a.edit(1) // the providers' table's row, which the rankers' tab does not use
	if a.rankers.editing != "a" {
		t.Fatalf("Edit… on the rankers' tab edits %q, want the row under its cursor, a", a.rankers.editing)
	}
}
