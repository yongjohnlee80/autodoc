package tui

// Copies of the agents' test fakes agentstartup_own_test.go declares, for the host's cells.

// A crew Claude agent is past its startup once Claude Code ran its SessionStart hook, which
// registers its endpoint; before that it may be showing its folder-trust question, whose default
// is "No, exit". Its cells: claudeOne is crewAgents with one Claude agent, cl, started.
const claudeOneCopy = `[{"name":"Claude","command":"one","kind":"claude","codename":"cl"}]`
