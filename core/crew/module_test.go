package crew_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// TestTheModuleDeclaresItsVerbs: with no store (as --contracts builds it) the module answers its
// name, verbs and errors; every contract passes its own check, has an effect, and names only
// codes the module declares; and the codes are in the module's range (module.CheckCodes).
func TestTheModuleDeclaresItsVerbs(t *testing.T) {
	m := crew.New(nil)
	if err := module.CheckCodes(m); err != nil {
		t.Fatal(err)
	}
	declared := map[int64]bool{}
	for _, e := range m.Errors() {
		declared[e.Code] = true
	}
	seen := map[string]bool{}
	for _, v := range m.Verbs() {
		c := v.Contract
		if err := c.Check(); err != nil {
			t.Errorf("%s: %v", c.Verb, err)
		}
		if c.Effect == module.EffectUnset {
			t.Errorf("%s has no effect", c.Verb)
		}
		if seen[c.Verb] {
			t.Errorf("%s declared twice", c.Verb)
		}
		seen[c.Verb] = true
		for _, code := range c.Errors {
			if !declared[code] {
				t.Errorf("%s answers %d, which Errors does not declare", c.Verb, code)
			}
		}
	}
	for _, want := range []string{"crew.claim", "crew.takeover", "crew.release", "crew.register", "crew.renew", "crew.exit"} {
		if !seen[want] {
			t.Errorf("no %s", want)
		}
	}
}
