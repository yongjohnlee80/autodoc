package manifests

import "testing"

// FuzzParsePlugin: no plugin.toml panics ParsePlugin, and a manifest that parses lists what it
// declares without panicking.
func FuzzParsePlugin(f *testing.F) {
	f.Add(head)
	f.Add(openHead + "[open]\nextensions = [\".ipynb\"]\ncommand = \"import\"\n")
	f.Add("name = \"svc\"\nkind = \"service\"\nprotocol = 4\ncommand = [\"./svc\"]\nstart = \"launch\"\ncalls = [\"todos.open\"]\n[feed]\ndocument = true\n[table]\ntitle = \"Results\"\n[[commands]]\nid = \"run\"\ntitle = \"Run\"\nkey = \"r\"\n")
	f.Add(head + "[dialog]\nmodal = false\nwidth = 9999\nheight = \"50%\"\n")
	f.Add("name = ")
	f.Fuzz(func(t *testing.T, src string) {
		m, err := ParsePlugin([]byte(src))
		if err != nil || m == nil {
			return
		}
		_ = m.Declares()
	})
}
