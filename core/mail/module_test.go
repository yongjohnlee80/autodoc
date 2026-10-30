package mail_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// TestTheModuleDeclaresItsVerbs: with no store (as --contracts builds it) the module answers its
// name, verbs and errors; every contract passes its own check, has an effect, and names only codes
// mail or crew declares (a mail verb answers crew's bad credential and stale epoch); and mail's
// codes are in its range.
func TestTheModuleDeclaresItsVerbs(t *testing.T) {
	m := mail.New(nil, nil)
	if err := module.CheckCodes(m); err != nil {
		t.Fatal(err)
	}
	declared := map[int64]bool{}
	for _, mod := range []module.Module{m, crew.New(nil)} {
		for _, e := range mod.Errors() {
			declared[e.Code] = true
		}
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
				t.Errorf("%s answers %d, which neither mail nor crew declares", c.Verb, code)
			}
		}
	}
	for _, want := range []string{"mail.send", "mail.receive", "mail.renew", "mail.ack", "mail.reply", "mail.inbox"} {
		if !seen[want] {
			t.Errorf("no %s", want)
		}
	}
}
