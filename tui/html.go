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

// openDefaultApp hands path to the desktop's default app for its kind (an HTML file to the
// browser, a PDF to its reader): xdg-open, or open on a Mac.
//
// The opener is started in a session of its own and never killed. Where no desktop environment
// runs one (Hyprland, sway), xdg-open starts the app itself and returns only when it closes, so a
// timeout that killed it closed the app it had just opened. An opener that fails at once (no app
// for the kind) is an error; one still running after openerGrace is the app, left running, its
// exit collected when it comes.
func openDefaultApp(ctx context.Context, path string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	return startOpener(ctx, command, path)
}

// startOpener runs command on path as openDefaultApp says: detached, an early failure returned,
// a command still running after openerGrace left running.
func startOpener(ctx context.Context, command string, args ...string) error {
	cmd := exec.Command(command, args...)
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(openerGrace):
		return nil
	case <-ctx.Done():
		return nil
	}
}

// openerGrace is how long an opener has to fail before it is taken to be running the app.
const openerGrace = 2 * time.Second

// openWithDefaultApp opens the open file itself, on disk, with the desktop's default app for its
// kind: a PDF in its reader, for the diagrams and layout its derived text cannot show.
func (h *Host) openWithDefaultApp() {
	if !h.file.open {
		h.notify("no file is open: open one, then SPC O opens it with its default app")
		return
	}
	full := h.diskPath()
	if full == "" {
		h.notify("the workspace's folder is not known yet: try again once it is listed")
		return
	}
	do(h, func(ctx context.Context) error { return h.browser(ctx, full) }, func(err error) {
		if err != nil {
			h.notify("open with the default app: " + err.Error())
			return
		}
		h.say("opened " + h.file.path + " with its default app")
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
	source, theme := []byte(h.core.Value()), h.exportTheme()
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
