package tui

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// SPC a i opens the inbox: the agents' card's i, which no other key of the card takes, in the
// keymap every build reads (the registry refuses a key taken twice; this file builds in the GUI
// build too); the inbox command is :inbox.
func TestSPCaiOpensTheInbox(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	fs := h.features()
	if _, err := feature.NewRegistry(fs...); err != nil {
		t.Fatal(err)
	}
	var on []feature.Key
	for _, k := range feature.KeyCatalog(fs).On(agentsCard) {
		if k.Seq == "i" {
			on = append(on, k)
		}
	}
	if len(on) != 1 || on[0].Run != "App.openInbox()" {
		t.Fatalf("SPC a i: %+v, want one key, opening the inbox", on)
	}
	var verb string
	for _, f := range fs {
		if c, ok := f.(interface{ Catalog() []feature.Entry }); ok {
			for _, e := range c.Catalog() {
				if e.ID == "crew.inbox" {
					verb = e.Verb
				}
			}
		}
	}
	if verb != "inbox" {
		t.Fatalf("the inbox's command: %q, want :inbox", verb)
	}
}
