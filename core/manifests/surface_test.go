package manifests

import (
	"strings"
	"testing"
)

// [surface] is protocol 10's: refused below it and on a service, its context_menu host or plugin,
// host when unset.
func TestASurfaceNeedsProtocolTenAndSomethingToDrawIn(t *testing.T) {
	head := "name = \"pix\"\nkind = \"dialog\"\ncommand = [\"./pix\"]\n"
	for name, c := range map[string]struct {
		src, refusal string
	}{
		"protocol 9":      {head + "protocol = 9\n[surface]\n", "needs protocol = 10"},
		"a service":       {"name = \"pix\"\nkind = \"service\"\ncommand = [\"./pix\"]\nprotocol = 10\n[surface]\n", "a service has nothing to draw in"},
		"an unknown menu": {head + "protocol = 10\n[surface]\ncontext_menu = \"both\"\n", "want host or plugin"},
		"an unknown key":  {head + "protocol = 10\n[surface]\nvsync = true\n", "unknown key"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePlugin([]byte(c.src)); err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Errorf("err %v, want it refused: %s", err, c.refusal)
			}
		})
	}
	m, err := ParsePlugin([]byte(head + "protocol = 10\n[surface]\ntext_input = true\n"))
	if err != nil || m.Surface == nil || m.Surface.ContextMenu != "host" || !m.Surface.TextInput {
		t.Fatalf("the positive control: %+v, %v", m.Surface, err)
	}
}
