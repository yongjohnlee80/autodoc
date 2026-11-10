package relations

import (
	"flag"
	"image/color"
	"os"
	"sync"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/feature/featuretest"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// update rewrites the goldens (testdata/) from what the views draw now.
var update = flag.Bool("update", false, "rewrite the goldens under testdata/")

// english is the host's English catalog (tui/qml/i18n), whose words the views read.
var english = sync.OnceValue(func() func(string) string { return featuretest.EnglishFrom(os.DirFS("../qml"), "i18n") })

// englishText is id's English, or id when the catalog has none.
func englishText(id string) string { return english()(id) }

// englishf is id's English with its %1, %2 … filled by args.
func englishf(id string, args ...any) string { return feature.Fill(englishText(id), args...) }

// newScripted is a scripted Core in workspace ws answering in the host's English.
func newScripted(ws string) *featuretest.ScriptedCore {
	c := featuretest.NewScripted(ws)
	c.UseText(englishText)
	return c
}

// press is a key pressed: code, and the text it types.
func press(code rune, text string) tuicore.KeyEvent {
	return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: code, Text: text}
}

// rgb is v (0xRRGGBB), opaque.
func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// mapPrefs are preferences of the values v alone.
type mapPrefs map[string]string

func (p mapPrefs) Value(name string) string              { return p[name] }
func (mapPrefs) PanelGeometry(string, string) (int, int) { return 0, 0 }
