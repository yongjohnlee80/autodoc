package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// exportMain writes sourcePath exported to destination; its relative links are read from base, or
// from the source's own directory when base is "", so the page reaches them wherever it is written.
func exportMain(sourcePath, format, destination, theme, base string) error {
	if sourcePath == "" || destination == "" {
		return errors.New("--export needs a Markdown source and --output destination")
	}
	if strings.ToLower(filepath.Ext(sourcePath)) != ".md" {
		return errors.New("--export currently accepts Markdown (.md) sources")
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if destinationInfo, err := os.Stat(destination); err == nil && os.SameFile(sourceInfo, destinationInfo) {
		return errors.New("export destination is the source file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if base == "" {
		base = filepath.Dir(sourcePath)
	} else if baseInfo, err := os.Stat(base); err != nil {
		return fmt.Errorf("--base: %w", err)
	} else if !baseInfo.IsDir() {
		return fmt.Errorf("--base %s is not a directory", base)
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	content, err := export.RenderWith(source, export.Format(format), theme, export.Options{Base: base})
	if err != nil {
		return fmt.Errorf("%s: %w", sourcePath, err)
	}
	return export.WriteFile(destination, content)
}
