package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// hintProbe is the start of the normal-mode hint as the strip draws it (terminalkeymap.go): the mode
// key's name, which a narrow strip keeps when it cuts the rest.
const hintProbe = "Ctrl+T or i"

// The agent panel's terminals follow the keymap too: under the text editor's keys, its normal mode
// says how to get back.
func TestATextUsersAgentNormalModeSaysHowToGetBack(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runSetupTUI(t, agentsFolder(t), []agents.AgentProfile{{Name: "fake", Command: agentCall(agent)}}, "fake",
		map[string]string{prefKeymap: "text"}, Options{})
	onLoop(r, func() bool { r.h.agents.ToggleAgent(); return true }) // SPC g: the text editor's keys have no SPC
	r.s.WaitForText(t, "agent · fake")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	r.keys(t, ctrlKey('t'))
	r.s.WaitForText(t, hintProbe)
	r.keys(t, ctrlKey('t'))
	r.s.WaitFor(t, "back to typing", func(sc string) bool { return !strings.Contains(sc, hintProbe) })
}
