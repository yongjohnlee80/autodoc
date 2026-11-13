// Package pluginproto is the plugin protocol's versions: the range of versions a plugin's manifest
// may declare that this AutoDoc runs (core/manifests checks it), and the range the plugin SDK
// speaks (plugin.MinProtocol and plugin.Protocol are these). One source for both, so the host and
// the SDK it ships with never disagree, and core has no need of the SDK to know it.
//
// It imports nothing: the SDK imports it, and a plugin built on the SDK carries no more of AutoDoc
// than these constants.
package pluginproto

// The protocol's versions, from Min to Current. The protocol rises when a manifest can declare
// something an older host must not run without, for a changed meaning, or for something a plugin
// must handle (docs/decisions/plugins.md#protocol-versions-and-strict-manifests). Each version has
// its own plugin/protocolN_test.go.
const (
	// Min is the oldest version a host runs and the SDK answers.
	Min = 1
	// Current is the newest version, the one a new manifest declares.
	//
	// 11: [[menu]] and host.menu (docs/decisions/plugins.md#editor-menu-rows);
	// 10: [surface] (docs/decisions/plugins.md#pixel-surfaces)
	Current = 11
)
