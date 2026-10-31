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
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return "", err
	}
	return string(bytes.TrimSuffix(b.Bytes(), []byte("\n"))), nil
}
