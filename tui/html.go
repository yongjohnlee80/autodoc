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

// openSystemViewer opens the open file itself, on disk, in the desktop's own viewer: a PDF in its
// reader, for the diagrams and layout its derived text cannot show.
func (h *Host) openSystemViewer() {
	if !h.file.open {
		h.notify("no file is open: open one, then SPC O opens it in the system viewer")
		return
	}
	full := h.diskPath()
	if full == "" {
		h.notify("the workspace's folder is not known yet: try again once it is listed")
		return
	}
	do(h, func(ctx context.Context) error { return h.browser(ctx, full) }, func(err error) {
		if err != nil {
			h.notify("open in the system viewer: " + err.Error())
			return
		}
		h.say("opened " + h.file.path + " in the system viewer")
	})
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
	// the page is written to the cache: its relative links are read from the file's own folder, so
	// they reach the files beside it. An untitled draft's are written as it has them
	var options export.Options
	if full := h.diskPath(); full != "" {
		options.Base = filepath.Dir(full)
	}
	image, why := h.imageMode()
	h.say("opening HTML preview…")
	type answer struct {
		content []byte
		path    string
		err     error
	}
	do(h, func(ctx context.Context) answer {
		content, err := export.RenderWith(source, export.HTML, theme, options)
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
