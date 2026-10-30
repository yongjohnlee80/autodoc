package rpc

import (
	"context"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// TestADamagedStoreRefusesWritesAndKeepsReads (AM-4): once a check found the store damaged, every
// verb that writes it is refused (rpc.store_damaged, saying why); reads go on, and so does the
// daemon's own sys.* (sys.shutdown must still stop it).
func TestADamagedStoreRefusesWritesAndKeepsReads(t *testing.T) {
	r := serve(t)
	cli, _, err := helloAs(r, Protocol, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	r.srv.RefuseWrites("store: autodoc.db is damaged")
	_, err = cli.Call(context.Background(), "preference.set", "a", "b")
	if c, msg := wireError(err); c != CodeStoreDamaged || !strings.Contains(msg, "damaged") {
		t.Fatalf("preference.set on a damaged store: %d %q, want rpc.store_damaged", c, msg)
	}
	if _, err := cli.Call(context.Background(), "workspace.list"); err != nil {
		t.Fatalf("workspace.list on a damaged store: %v, want it answered", err)
	}
	for verb, want := range map[string]bool{"sys.shutdown": false, "sys.profile": false, "preference.set": true} {
		if got := writes(verb, r.srv.verbs[verb].Effect); got != want {
			t.Errorf("%s (%v): writes %v, want %v", verb, r.srv.verbs[verb].Effect, got, want)
		}
	}
	if writes("doc.write", module.EffectWrite) != true || writes("doc.read", module.EffectRead) != false {
		t.Error("a write is not a write, or a read is")
	}
}
