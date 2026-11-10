package feature

import "encoding/json"

// THE GRANTS: what the user let each holder do, kept in one preference (PrefGrants). A holder is a
// gated Lua script, a plugin's Lua package (plugin:<name>) or an agent (agent:<codename>, the editor
// socket's). The host's gate asks for and checks them; Agent permissions lists and revokes them.

// PrefGrants is the grants' preference: a JSON map, holder → capability → true.
const PrefGrants = "lua.grants"

// CapabilityLaunch is a plugin's start with AutoDoc: its manifest's start = "launch".
const CapabilityLaunch = "launch"

// GrantsOf reads PrefGrants' value; anything else is no grants (nil).
func GrantsOf(s string) map[string]map[string]bool {
	var g map[string]map[string]bool
	if json.Unmarshal([]byte(s), &g) != nil {
		return nil
	}
	return g
}

// CapabilityText is each capability's words, by its catalog id: what it lets its holder (a gated
// script, a plugin's package, an agent) do.
var CapabilityText = map[string]string{
	"workspace.read":  "autodoc.question.luagate.read_the_files_in_your_workspaces_except_hidden",
	"workspace.write": "autodoc.question.luagate.create_edit_rename_and_remove_the_files_of_this",
	"settings":        "autodoc.question.luagate.change_autodoc_s_settings_workspaces_models_pref",
	"clipboard":       "autodoc.question.luagate.read_and_write_the_clipboard",
	"agents":          "autodoc.question.luagate.list_the_agents_and_send_them_text_an_agent_can",
	"commands":        "autodoc.question.luagate.run_any_of_autodoc_s_commands",
	"terminal":        "autodoc.question.luagate.run_command_lines_in_autodoc_s_terminal",
	// a plugin's start with AutoDoc, its manifest's start = "launch" (the host's pluginlaunch.go)
	CapabilityLaunch: "autodoc.question.pluginlaunch.start_with_autodoc",
	// the editor socket's (the host's editorsocket.go): an agent's reach into the page
	"editor.read":  "autodoc.question.luagate.read_your_open_documents_and_find_in_them",
	"editor.write": "autodoc.question.luagate.insert_replace_and_delete_text_in_your_open_documents",
}
