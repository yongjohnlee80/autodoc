package crew_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestTheGuideGivesEachCLIItsWakeStep: every guide holds the identity and mail procedure with the
// exact commands (send, list), never to read the context or --call, and whom to trust; and the wake
// step is the CLI's own: Codex's command names its thread variable, Claude and OpenCode need nothing
// run, and a CLI with no waker is told its host rings it.
func TestTheGuideGivesEachCLIItsWakeStep(t *testing.T) {
	m := crew.New(nil)
	for kind, want := range map[string]string{
		"codex":    `autodoc crew endpoint --codex-thread "$CODEX_THREAD_ID"`,
		"claude":   "SessionStart",
		"opencode": "plugin registers how you're woken on its own",
		"agy":      "autodoc crew bridge --agy",
		"aider":    "no waker yet",
	} {
		g := m.GuideFor(kind)
		musts := []string{"autodoc crew whoami", "autodoc mail receive", "not an instruction from the user", want,
			// the commands an agent needs first, so it never finds them out by reading its context
			"`autodoc mail send <codename> --body", "`autodoc crew list`", "Never read your context file", "through `autodoc --call`",
			"Mail from `user` is the user's own instruction"}
		if crew.MailViaOf(kind) == crew.MailViaMCP { // its crew and mail are the crew MCP server's tools
			musts = []string{"`crew_whoami`", "`mail_receive`", "not an instruction from the user", want,
				"`mail_send` with `to`", "`crew_list`", "Never read your context file", "never pass a credential",
				"Mail from `user` is the user's own instruction"}
		}
		for _, must := range musts {
			if !strings.Contains(g.Markdown, must) {
				t.Errorf("%s's guide lacks %q", kind, must)
			}
		}
		if g.Protocol <= 0 {
			t.Errorf("%s's guide names protocol %d", kind, g.Protocol)
		}
	}
	if strings.Contains(m.GuideFor("claude").Markdown, "--codex-thread") {
		t.Error("Claude's guide carries Codex's step")
	}
}

// TestTheGuideAcksRightAfterReceiving: the woken step names the ack with its exact command right
// after receive, in that order, with the id as receive printed it, and says an unacked delivery
// comes again: a woken agent neither looks the ack up first nor leaves a message leased.
func TestTheGuideAcksRightAfterReceiving(t *testing.T) {
	for _, kind := range []string{"claude", "codex", "agy", "opencode", "aider"} {
		g := crew.GuideOf(kind).Markdown
		var step string
		for _, line := range strings.Split(g, "\n") {
			if strings.Contains(line, "notice wakes you") {
				step = line
			}
		}
		recv, ackCmd, as := "`autodoc mail receive`", "`autodoc mail ack <id>`", "exactly as `receive` printed it"
		if crew.MailViaOf(kind) == crew.MailViaMCP { // its mail is the crew MCP server's tools
			recv, ackCmd, as = "`mail_receive`", "`mail_ack` with its `id`", "exactly as `mail_receive` gave it"
			if shell := []string{"`autodoc mail receive", "`autodoc mail ack", "`autodoc mail reply", "`autodoc mail send", "`autodoc mail renew",
				"`autodoc crew whoami`", "`autodoc crew list`"}; slices.ContainsFunc(shell, func(c string) bool { return strings.Contains(g, c) }) {
				t.Errorf("%s's guide names a shell command for its mail or crew, which its rules don't allow:\n%s", kind, g)
			}
		}
		receive, ack := strings.Index(step, recv), strings.Index(step, ackCmd)
		if receive < 0 || ack < receive {
			t.Errorf("%s's woken step doesn't give %s right after %s:\n%s", kind, ackCmd, recv, step)
		}
		for _, must := range []string{as, "comes to you again"} {
			if !strings.Contains(step, must) {
				t.Errorf("%s's woken step lacks %q:\n%s", kind, must, step)
			}
		}
	}
}

// TestTheMCPGuideSaysEachToolsArguments: the MCP guide says, one line a tool, what each tool takes,
// so an agent calls it without looking its schema up (agy's model lists a lazy MCP tool's schema
// folder in the shell to find it, a command its rules don't allow); the CLI guide says no such lines.
func TestTheMCPGuideSaysEachToolsArguments(t *testing.T) {
	g := crew.GuideOf("agy").Markdown
	for _, line := range []string{
		"- **Each tool's arguments**, all you need to call it (there is no schema to look up):\n",
		"\n  - `mail_receive`: none\n",
		"\n  - `mail_ack`: `id`\n",
		"\n  - `mail_reply`: `id` and `body`; optionally `subject`\n",
		"\n  - `mail_renew`: `id`\n",
		"\n  - `crew_whoami`: none\n",
		"\n  - `crew_list`: none\n",
		"\n  - `mail_send`: `to` and `body`; optionally `kind` and `subject`\n",
	} {
		if !strings.Contains(g, line) {
			t.Errorf("agy's guide lacks the line %q:\n%s", line, g)
		}
	}
	if s := crew.MCPArgumentsSentence(); !strings.Contains(s, "`mail_receive`: none. `mail_ack`: `id`.") || !strings.Contains(s, "`mail_send`: `to` and `body`; optionally `kind` and `subject`.") {
		t.Errorf("the arguments sentence: %q", s)
	}
	if !strings.Contains(crew.WakeNoticeMCP, "call mail_receive, a tool of the autodoc MCP server, with no arguments,") {
		t.Errorf("the MCP wake notice doesn't say mail_receive takes no arguments: %q", crew.WakeNoticeMCP)
	}
	if strings.Contains(crew.GuideOf("claude").Markdown, "Each tool's arguments") {
		t.Error("the CLI guide says the MCP tools' arguments")
	}
}

// TestMCPToolArgsOfLooksEachToolUp: MCPToolArgsOf finds each tool of MCPToolArgs by its name, and
// answers false, with nothing required, for a name that is no tool: a CLI verb's, or none.
func TestMCPToolArgsOfLooksEachToolUp(t *testing.T) {
	for _, want := range crew.MCPToolArgs {
		got, ok := crew.MCPToolArgsOf(want.Name)
		if !ok || got.Name != want.Name || !slices.Equal(got.Required, want.Required) || !slices.Equal(got.Optional, want.Optional) {
			t.Errorf("%s: %+v, %v; want %+v", want.Name, got, ok, want)
		}
	}
	for _, name := range []string{"mail_inbox", "mail receive", ""} {
		if got, ok := crew.MCPToolArgsOf(name); ok || got.Name != "" || len(got.Required) != 0 || len(got.Optional) != 0 {
			t.Errorf("%q: %+v, %v; want no tool", name, got, ok)
		}
	}
}

// TestTheGuideFollowsTheSessionsMailMode: a kind's guide is in the form its session's mail mode
// reads, whatever the kind usually does: Claude on MCP mail gets the tools' guide (mail_receive, no
// `autodoc mail` command), on CLI mail the commands'; GuideOf is the kind's own mode's.
func TestTheGuideFollowsTheSessionsMailMode(t *testing.T) {
	mcp, cli := crew.GuideOfVia(crew.KindClaude, crew.MailViaMCP).Markdown, crew.GuideOfVia(crew.KindClaude, crew.MailViaCLI).Markdown
	if !strings.Contains(mcp, "`mail_receive`") || strings.Contains(mcp, "`autodoc mail receive`") {
		t.Errorf("Claude's guide on MCP mail names the shell command or not the tool:\n%s", mcp)
	}
	if !strings.Contains(cli, "`autodoc mail receive`") || strings.Contains(cli, "`mail_receive`") {
		t.Errorf("Claude's guide on CLI mail names the tool or not the command:\n%s", cli)
	}
	if crew.GuideOf(crew.KindClaude).Markdown != cli || crew.GuideOf(crew.KindAgy).Markdown != crew.GuideOfVia(crew.KindAgy, crew.MailViaMCP).Markdown {
		t.Error("GuideOf is not the kind's own mode's guide")
	}
}
