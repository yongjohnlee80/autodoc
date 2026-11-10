package searchsetup

import (
	"os"
	"sync"

	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
)

// english is the host's English catalog (tui/qml/i18n), whose words the cells read.
var english = sync.OnceValue(func() func(string) string { return featuretest.EnglishFrom(os.DirFS("../qml"), "i18n") })

// englishText is id's English, or id when the catalog has none.
func englishText(id string) string { return english()(id) }

// newScripted is a scripted Core in workspace ws answering in the host's English.
func newScripted(ws string) *featuretest.ScriptedCore {
	c := featuretest.NewScripted(ws)
	c.UseText(englishText)
	return c
}
