package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/export"
)

func exportMain(sourcePath, format, destination, theme string) error {
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
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	content, err := export.Render(source, export.Format(format), theme)
	if err != nil {
		return fmt.Errorf("%s: %w", sourcePath, err)
	}
	return export.WriteFile(destination, content)
}
