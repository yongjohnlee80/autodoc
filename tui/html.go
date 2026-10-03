package tui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/yongjohnlee80/autodoc/core/export"
)

func openDefaultBrowser(ctx context.Context, path string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, command, path).Run()
}

func htmlPreviewFile(content []byte) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(cache, "autodoc", "previews")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, fmt.Sprintf("%x.html", sha256.Sum256(content)))
	if err := export.WriteFile(path, content); err != nil {
		return "", err
	}
	return path, nil
}

func (h *Host) previewHTML() {
	if h.note.open && filepath.Ext(h.note.path) != ".md" {
		h.notify("HTML preview currently accepts Markdown notes")
		return
	}
	source, theme := []byte(h.editor.Value()), h.theme
	if theme != "light" {
		theme = "dark"
	}
	h.say("opening HTML preview…")
	do(h, func(ctx context.Context) error {
		content, err := export.Render(source, export.HTML, theme)
		if err != nil {
			return err
		}
		path, err := htmlPreviewFile(content)
		if err != nil {
			return err
		}
		return h.browser(ctx, path)
	}, func(err error) {
		if err != nil {
			h.notify("HTML preview: " + err.Error())
			return
		}
		h.say("HTML preview opened in browser")
	})
}
