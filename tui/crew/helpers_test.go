package crew

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
	"github.com/yongjohnlee80/golib/tui/i18n"
)

// englishText is id's English, or id when the catalog has none: the catalog the program's words come
// from (tui/qml/i18n), read from the module's files, as the crew mail's cells say them.
func englishText(id string) string {
	if t, ok := testCatalog().Lookup(i18n.English, id); ok {
		return t
	}
	return id
}

// trf is englishText with its %1, %2 … filled with args, as the program fills a sentence.
func trf(id string, args ...any) string { return feature.Fill(englishText(id), args...) }

// testCatalog is the English catalog, found from the module's root above the test's folder.
var testCatalog = sync.OnceValue(func() *i18n.Set {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		if filepath.Dir(dir) == dir {
			panic("the crew mail's cells: no go.mod above the test's folder")
		}
		dir = filepath.Dir(dir)
	}
	s := &i18n.Set{}
	if err := s.LoadDir(os.DirFS(filepath.Join(dir, "tui", "qml")), "i18n", "autodoc"); err != nil {
		panic("the crew mail's cells: the English catalog: " + err.Error())
	}
	return s
})

// testPrefs is the preferences a fake window answers (feature.PrefsView and its writer): each value by
// its name, no panel's geometry.
type testPrefs struct{ values map[string]string }

func (p *testPrefs) Value(name string) string { return p.values[name] }

func (p *testPrefs) SetValue(name, v string) {
	if p.values == nil {
		p.values = map[string]string{}
	}
	p.values[name] = v
}

func (*testPrefs) PanelGeometry(string, string) (int, int) { return 0, 0 }

// grantsPrefs is the preferences holding grants, as the program stores them: their JSON, under
// feature.PrefGrants (none when there are none).
func grantsPrefs(grants map[string]map[string]bool) *testPrefs {
	p := &testPrefs{}
	if len(grants) > 0 {
		b, _ := json.Marshal(grants)
		p.SetValue(feature.PrefGrants, string(b))
	}
	return p
}

// newScripted is featuretest's Core answering in English, with the agents' unavailable error, as the
// crew mail's cells were written against.
func newScripted(ws string) *featuretest.ScriptedCore {
	c := featuretest.NewScripted(ws)
	c.Preferences = &testPrefs{}
	c.Text, c.Textf, c.AgentErr = englishText, trf, agents.ErrAgentUnavailable
	return c
}
