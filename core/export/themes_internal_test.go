package export

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/diagram"
)

// Every shipped theme exports with its own colours and the color scheme that suits them.
func TestEveryThemeExportsInItsOwnColours(t *testing.T) {
	for theme, scheme := range map[string]string{"light": "light", "dark": "dark", "sepia": "light", "retro": "dark", "mono": "light"} {
		out, err := Render([]byte("# T\n\ntext\n"), HTML, theme)
		if err != nil {
			t.Fatalf("%s: %v", theme, err)
		}
		if !strings.Contains(string(out), `<meta name="color-scheme" content="`+scheme+`">`) ||
			!strings.Contains(string(out), "--background:"+palettes[theme].background) {
			t.Errorf("%s: not its scheme %s and background %s", theme, scheme, palettes[theme].background)
		}
	}
	if _, err := Render([]byte("x"), HTML, "neon"); err == nil {
		t.Fatal("an unknown theme exported")
	}
	if ThemeOf("sepia") != "sepia" || ThemeOf("unknown") != "dark" {
		t.Fatal("ThemeOf")
	}
}

// A standalone diagram SVG carries its own background and font, in the theme's colours.
func TestDiagramSVGIsStandalone(t *testing.T) {
	model, err := diagram.Parse("flowchart LR\n  A[Start] --> B[End]\n")
	if err != nil {
		t.Fatal(err)
	}
	svg, err := DiagramSVG(model, "sepia")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(svg, `<svg `) || !strings.HasSuffix(svg, "</svg>") ||
		!strings.Contains(svg, `<rect width="100%" height="100%" fill="#f4ecd8"/>`) || !strings.Contains(svg, "font-family") ||
		!strings.Contains(svg, ">Start<") {
		t.Fatalf("svg = %s", svg)
	}
	if _, err := DiagramSVG(model, "neon"); err == nil {
		t.Fatal("an unknown theme")
	}
}
