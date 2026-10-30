package crew_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// TestNamesAndIdentifiersAreBoundedAtAdmission: every name and identifier that travels in the
// human's feed is bounded where it enters (a codename or kind at 64 bytes; a scope, instance or
// workspace at 256), so one feed entry always fits a page: past the bound is refused, never
// truncated, and the bound itself is accepted.
func TestNamesAndIdentifiersAreBoundedAtAdmission(t *testing.T) {
	ctx := context.Background()
	m := crew.New(crewtest.NewMem(crewtest.NewClock().Now))
	if _, err := m.Claim(ctx, "autodoc:"+strings.Repeat("s", crew.MaxIdent), "run-a", ""); !errors.Is(err, crew.ErrInvalid) {
		t.Fatalf("a scope past %d bytes: %v, want ErrInvalid", crew.MaxIdent, err)
	}
	scope := "autodoc:" + strings.Repeat("s", crew.MaxIdent-len("autodoc:"))
	e, err := m.Claim(ctx, scope, "run-a", "")
	if err != nil {
		t.Fatalf("a scope of exactly %d bytes: %v", crew.MaxIdent, err)
	}
	reg := func(codename, kind, instance, workspace string) error {
		_, err := m.Register(ctx, crew.Registration{Scope: scope, Run: "run-a", Epoch: e, Codename: codename, Kind: kind,
			Instance: instance, Workspace: workspace, Secret: strings.Repeat("x", 32) + instance})
		return err
	}
	long := func(n int) string { return strings.Repeat("a", n) }
	for name, err := range map[string]error{
		"a 16 KiB codename":       reg(long(16<<10), "claude", "i1", "w"),
		"a codename one too long": reg(long(crew.MaxName+1), "claude", "i2", "w"),
		"a kind one too long":     reg("gold", long(crew.MaxName+1), "i3", "w"),
		"an instance too long":    reg("gold", "claude", long(crew.MaxIdent+1), "w"),
		"a workspace too long":    reg("gold", "claude", "i4", long(crew.MaxIdent+1)),
	} {
		if !errors.Is(err, crew.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if err := reg(long(crew.MaxName), long(crew.MaxName), long(crew.MaxIdent), long(crew.MaxIdent)); err != nil {
		t.Fatalf("every field at its bound: %v", err)
	}
}
