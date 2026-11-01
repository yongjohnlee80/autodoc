package crew

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// opencodePlugin is AutoDoc's OpenCode plugin
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): it registers its session and wakes it
// when asked (crew.OpenCodeWaker).
//
//go:embed opencode/autodoc-crew.js
var opencodePlugin []byte

// OpenCodePluginName is the plugin's file name in its folder.
const OpenCodePluginName = "autodoc-crew.js"

// WriteOpenCodePlugin writes the plugin into dir, unless it is there already as it is, and answers
// its path. It goes through a temporary file renamed over it: an opencode starting at the moment
// loads the old plugin or the new, whole.
func WriteOpenCodePlugin(dir string) (string, error) {
	path := filepath.Join(dir, OpenCodePluginName)
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, opencodePlugin) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "."+OpenCodePluginName+".*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // after the rename, nothing is there: a no-op
	if _, err := f.Write(opencodePlugin); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// pluginURL is the file URL of the plugin at path: absolute, and escaped, so a folder whose name
// holds '#', '?', '%', a space or any non-ASCII names the very file (a URL's '#' starts a fragment
// and '%' an escape).
func pluginURL(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String(), nil
}

// OpenCodeWithAllow is content (an OPENCODE_CONFIG_CONTENT) with the guide's commands allowed
// (OpenCodeAllow) in its permission.bash, every rule already there kept: a session's own config,
// never the user's opencode.json. A bash that is one action for every command ("ask") stays that
// action for the rest ("*"); a permission that is no object is left as it is, with no allowance.
func OpenCodeWithAllow(content string) (string, error) {
	cfg := map[string]any{}
	d := json.NewDecoder(bytes.NewReader([]byte(content)))
	d.UseNumber()
	if err := d.Decode(&cfg); err != nil || cfg == nil {
		return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT is not a JSON object: %v", err)
	}
	perm, ok := cfg["permission"].(map[string]any)
	if !ok && cfg["permission"] != nil {
		return content, nil // not a shape to add rules to: the agent's own, untouched
	}
	if perm == nil {
		perm = map[string]any{}
	}
	bash, ok := perm["bash"].(map[string]any)
	if action, isAction := perm["bash"].(string); isAction {
		bash, ok = map[string]any{"*": action}, true
	}
	if !ok && perm["bash"] != nil {
		return content, nil // not a shape to add rules to: the agent's own, untouched
	}
	if bash == nil {
		bash = map[string]any{}
	}
	for rule, v := range OpenCodeAllow() {
		bash[rule] = v
	}
	perm["bash"], cfg["permission"] = bash, perm
	return encodeOpenCodeConfig(cfg)
}

// encodeOpenCodeConfig is cfg as OPENCODE_CONFIG_CONTENT: one line, its strings as written ('<',
// '>' and '&' unescaped).
func encodeOpenCodeConfig(cfg map[string]any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return "", err
	}
	return string(bytes.TrimSuffix(b.Bytes(), []byte("\n"))), nil
}

// OpenCodeWithMCP is OPENCODE_CONFIG_CONTENT v with servers in its mcp key, each a local server
// (its command and args as one list, its env, enabled): the user's own MCP servers are kept, and one
// of the same name is AutoDoc's.
func OpenCodeWithMCP(v string, servers []MCPServer) (string, error) {
	cfg := map[string]any{}
	d := json.NewDecoder(strings.NewReader(v))
	d.UseNumber()
	if err := d.Decode(&cfg); err != nil || cfg == nil {
		return "", fmt.Errorf("OPENCODE_CONFIG_CONTENT is not a JSON object: %v", err)
	}
	mcp, ok := cfg["mcp"].(map[string]any)
	if cfg["mcp"] != nil && !ok {
		return "", errors.New("OPENCODE_CONFIG_CONTENT's mcp is not an object")
	}
	if mcp == nil {
		mcp = map[string]any{}
	}
	for _, sv := range servers {
		if len(sv.Command) == 0 {
			return "", fmt.Errorf("crew: MCP server %q has no command", sv.Name)
		}
		server := map[string]any{"type": "local", "command": slices.Clone(sv.Command), "enabled": true}
		if len(sv.Env) > 0 {
			server["environment"] = sv.Env
		}
		mcp[sv.Name] = server
	}
	cfg["mcp"] = mcp
	return encodeOpenCodeConfig(cfg)
}

// OpenCodeConfigContent is $OPENCODE_CONFIG_CONTENT with the plugin at path in its plugin list:
// existing, a value already set (the user's, or another tool's), kept whole and its list extended,
// never replaced. OpenCode merges it over the user's own configuration. A plugin whose path holds a
// '?' is refused: opencode loads none from there, its URL escaped or not (by a live probe).
func OpenCodeConfigContent(existing, path string) (string, error) {
	if strings.ContainsRune(path, '?') {
		return "", fmt.Errorf("the plugin's folder %s holds a '?', and opencode loads no plugin from such a path: give --dir another folder", filepath.Dir(path))
	}
	cfg := map[string]any{}
	if existing != "" {
		d := json.NewDecoder(bytes.NewReader([]byte(existing)))
		d.UseNumber()
		if err := d.Decode(&cfg); err != nil || cfg == nil {
			return "", fmt.Errorf("$OPENCODE_CONFIG_CONTENT is set and is not a JSON object: %v", err)
		}
	}
	u, err := pluginURL(path)
	if err != nil {
		return "", err
	}
	var list []any
	switch p := cfg["plugin"].(type) {
	case nil:
	case []any:
		list = p
	default:
		return "", errors.New("$OPENCODE_CONFIG_CONTENT's plugin is not a list")
	}
	if !slices.Contains(list, any(u)) {
		list = append(list, u)
	}
	cfg["plugin"] = list
	return encodeOpenCodeConfig(cfg)
}
