package tui

import (
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

func formClick(t *testing.T, r *running, label string) {
	t.Helper()
	x, y := formAt(r.s.String(), label)
	if x < 0 {
		t.Fatalf("no %q on screen:\n%s", label, r.s.String())
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	time.Sleep(150 * time.Millisecond)
}

func openAddAgentForm(t *testing.T, profiles []agentProfile) *running {
	t.Helper()
	_, r, _ := runAgentTUI(t, t.TempDir(), profiles, profiles[0].Codename)
	onLoop(r, func() bool { r.h.agents.openAgentProfiles(); return true })
	r.s.WaitForText(t, "CODENAME")
	formClick(t, r, "[ Add… ]")
	r.s.WaitForText(t, "add an agent profile")
	return r
}

func storedProfile(r *running, codename string) (agentProfile, bool) {
	return onLoop(r, func() agentProfile { p, _ := r.h.agents.profile(codename); return p }),
		onLoop(r, func() bool { _, ok := r.h.agents.profile(codename); return ok })
}
