package tui

import (
	"slices"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// agenttabs_test.go: the agents' strip — tabs named by codename, + offering the profiles, a tab's
// menu going to another tab, restarting or closing its agent (asking while it runs).

// menuRow is the row id of rows (or of a submenu's), and whether it is there.
func menuRow(rows []widget.MenuItemModel, id string) (widget.MenuItemModel, bool) {
	for _, row := range rows {
		if string(row.ID) == id {
			return row, true
		}
		if found, ok := menuRow(row.Children, id); ok {
			return found, true
		}
	}
	return widget.MenuItemModel{}, false
}

// chooseRow runs the row id of rows, as the strip's menu does for tab.
func chooseRow(t *testing.T, rows []widget.MenuItemModel, id string, tab widget.TerminalTabID) {
	t.Helper()
	row, ok := menuRow(rows, id)
	if !ok {
		t.Fatalf("no row %s in %v", id, rowIDs(rows))
	}
	act, ok := row.Action.(widget.TerminalTabsMenuAction)
	if !ok || act.Run == nil || !row.Enabled {
		t.Fatalf("row %s cannot be chosen (enabled %v)", id, row.Enabled)
	}
	act.Run(tab)
}

func rowIDs(rows []widget.MenuItemModel) []string {
	var ids []string
	for _, row := range rows {
		ids = append(ids, string(row.ID))
		ids = append(ids, rowIDs(row.Children)...)
	}
	return ids
}

func TestAgentTabsAreNamedByCodenameAndPlusOffersTheProfiles(t *testing.T) {
	a, c := modelAgents(t)
	one, _ := a.profile("one")
	first, err := a.startInstance(one)
	if err != nil {
		t.Fatal(err)
	}
	if tabs := c.panel.Tabs(); len(tabs) != 1 || tabs[0].Label != "one" {
		t.Fatalf("tabs %+v: want one, named by its codename", tabs)
	}
	if c.panel.addMenu == nil || c.panel.tabMenu == nil {
		t.Fatal("the strip was given no menus")
	}
	rows := c.panel.addMenu()
	if got := rowIDs(rows); !slices.Equal(got, []string{"agents.add.one", "agents.add.two", "agents.add.profiles", "agents.profiles"}) {
		t.Fatalf("+ offers %v", got)
	}
	if row, _ := menuRow(rows, "agents.add.one"); row.Label != "One (one) ▶" {
		t.Errorf("a running profile's row is %q", row.Label)
	}
	// a stopped profile starts in a tab of its own; the first keeps running
	chooseRow(t, rows, "agents.add.two", "")
	second := a.instances["two"]
	if second == nil || !second.running || !first.running || len(c.panel.Tabs()) != 2 || c.panel.active != second.tab {
		t.Fatalf("+ two: instances %v, tabs %+v", a.instances, c.panel.Tabs())
	}
	// a running one is gone to, not started again
	chooseRow(t, c.panel.addMenu(), "agents.add.one", "")
	if len(c.panel.Tabs()) != 2 || c.panel.active != first.tab || a.selectedAgent != "one" {
		t.Fatalf("+ one: active %s (want %s), tabs %+v", c.panel.active, first.tab, c.panel.Tabs())
	}
	// Go to › on a tab's menu
	chooseRow(t, c.panel.tabMenu(first.tab), "tabs.goto."+string(second.tab), first.tab)
	if c.panel.active != second.tab || a.selectedAgent != "two" {
		t.Fatalf("Go to two: active %s, selected %s", c.panel.active, a.selectedAgent)
	}
}

func TestAgentTabMenuClosesAndRestartsAskingWhileItRuns(t *testing.T) {
	a, c := modelAgents(t)
	one, _ := a.profile("one")
	two, _ := a.profile("two")
	first, _ := a.startInstance(one)
	if _, err := a.startInstance(two); err != nil {
		t.Fatal(err)
	}
	rows := c.panel.tabMenu(first.tab)
	if got := rowIDs(rows); !slices.Contains(got, "agents.restart") || !slices.Contains(got, "agents.close") || !slices.Contains(got, "agents.profiles") {
		t.Fatalf("a tab's menu: %v", got)
	}

	// Close asks while the agent runs; only Yes closes it
	chooseRow(t, rows, "agents.close", first.tab)
	if len(c.questions) != 1 || a.instances["one"] != first || !first.running {
		t.Fatalf("Close of a running agent: %d questions, instance %v", len(c.questions), a.instances["one"])
	}
	c.questions[0].OnYes()
	if a.instances["one"] != nil || a.instances["two"] == nil {
		t.Fatalf("Close: instances %v", a.instances)
	}

	// Restart of a running agent asks, then starts it again in a new tab once the old is gone
	second := a.instances["two"]
	chooseRow(t, c.panel.tabMenu(second.tab), "agents.restart", second.tab)
	if len(c.questions) != 2 || a.instances["two"] != second {
		t.Fatalf("Restart of a running agent: %d questions", len(c.questions))
	}
	c.questions[1].OnYes()
	again := a.instances["two"]
	if again == nil || again == second || !again.running || again.tab == second.tab || again.token == second.token {
		t.Fatalf("Restart: %+v (old tab %s)", again, second.tab)
	}

	// Restart of a stopped one starts it in its own tab, without asking
	c.panel.exit(again.tab, 1)
	generation := again.generation
	chooseRow(t, c.panel.tabMenu(again.tab), "agents.restart", again.tab)
	if len(c.questions) != 2 || a.instances["two"] != again || !again.running || again.generation == generation {
		t.Fatalf("Restart of a stopped agent: %d questions, %+v", len(c.questions), again)
	}

	// Close of a stopped one closes without asking, and the panel's title says no agent
	c.panel.exit(again.tab, 1)
	chooseRow(t, c.panel.tabMenu(again.tab), "agents.close", again.tab)
	if len(c.questions) != 2 || a.instances["two"] != nil {
		t.Fatalf("Close of a stopped agent: %d questions, instance %v", len(c.questions), a.instances["two"])
	}
	c.panel.active = ""
	a.agentTabsChanged()
	if c.state["App.agentTitle"] != "agent" {
		t.Errorf("the title with no tab: %v", c.state["App.agentTitle"])
	}
}
