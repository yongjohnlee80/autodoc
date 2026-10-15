package app

import (
	"fmt"
	"io"
	"slices"

	"github.com/yongjohnlee80/autodoc/docs"
	"github.com/yongjohnlee80/autodoc/internal/usertheme"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

func checkTheme(file string, out io.Writer) error {
	if file == "" {
		return fmt.Errorf("--check-theme needs a .qml file")
	}
	v, err := usertheme.Check(file)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: valid Theme (%d required colours)\n", file, len(v))
	return nil
}

func newTheme(configPath, name, from string, out io.Writer) error {
	if name == "" {
		return fmt.Errorf("--new-theme needs a name")
	}
	dir, err := usertheme.Directory(configPath)
	if err != nil {
		return err
	}
	file, err := usertheme.Create(dir, name, from)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, file)
	return nil
}

func exportThemeFor(configPath, name string) (string, error) {
	if slices.Contains(themes.Names(), name) {
		return name, nil
	}
	dir, err := usertheme.Directory(configPath)
	if err != nil {
		return "", err
	}
	c := usertheme.Open(dir)
	if _, err := c.Values(name); err != nil {
		return "", err
	}
	return c.ExportStyle(name), nil
}

func themeReference(out io.Writer) { fmt.Fprint(out, docs.ThemeReference) }
