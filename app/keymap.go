package app

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/docs"
	"github.com/yongjohnlee80/autodoc/tui"
)

// keymapReference is --keymap-reference: docs/keymap.md, its table of commands the build's own.
func keymapReference(out io.Writer) { fmt.Fprint(out, docs.KeymapReference) }

// checkKeymap is --check-keymap: file, or keymap.xml beside the config, read as AutoDoc reads it. Every
// problem is the error; each warning is printed, and the file is still valid.
func checkKeymap(configPath, file string, out io.Writer) error {
	if file == "" {
		if configPath == "" {
			var err error
			if configPath, err = config.DefaultPath(); err != nil {
				return err
			}
		}
		file = filepath.Join(filepath.Dir(configPath), "keymap.xml")
	}
	warnings, err := tui.CheckKeymap(file)
	if err != nil {
		return errors.Join(fmt.Errorf("%s: not valid", file), err)
	}
	for _, w := range warnings {
		fmt.Fprintln(out, "warning:", w.String())
	}
	fmt.Fprintf(out, "%s: valid (%d warnings)\n", file, len(warnings))
	return nil
}
