package export

import (
	"strings"
	"testing"
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

// Each theme's diagrams take the mermaid theme of its scheme; mono's, neutral.
func TestEachThemesDiagramsTakeAMermaidTheme(t *testing.T) {
	for theme, want := range map[string]string{"dark": "dark", "retro": "dark", "light": "default", "sepia": "default", "mono": "neutral"} {
		out, err := DiagramPage("flowchart LR\n  A --> B\n", theme)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), `data-mermaid="`+want+`"`) || !strings.Contains(string(out), "--background:"+palettes[theme].background) {
			t.Errorf("%s: not mermaid's %s theme on its own background", theme, want)
		}
	}
	if _, err := DiagramPage("flowchart LR", "neon"); err == nil {
		t.Fatal("an unknown theme")
	}
}
