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
	if h.file.open && filepath.Ext(h.file.path) != ".md" {
		h.notify("HTML preview currently accepts Markdown files")
		return
	}
	source, theme := []byte(h.editor.Value()), h.exportTheme()
	image, why := h.imageMode()
	h.say("opening HTML preview…")
	type answer struct {
		content []byte
		path    string
		err     error
	}
	do(h, func(ctx context.Context) answer {
		content, err := export.Render(source, export.HTML, theme)
		if err != nil {
			return answer{err: err}
		}
		path, err := htmlPreviewFile(content)
		if err != nil {
			return answer{err: err}
		}
		if !image {
			err = h.browser(ctx, path)
		}
		return answer{content: content, path: path, err: err}
	}, func(a answer) {
		if a.err != nil {
			h.notify("HTML preview: " + a.err.Error())
			return
		}
		if image {
			h.previewHTMLImage(a.content, a.path) // in the terminal (preview.go)
			return
		}
		h.htmlPreviewPath = a.path
		h.say("HTML preview opened in browser · " + why)
	})
}
