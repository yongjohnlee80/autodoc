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

	"github.com/yongjohnlee80/autodoc/internal/localfs"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/userdirs"
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
		// NOTE: an opener (xdg-open, open) either fails at once, which is worth reporting, or hands
		// the file to an application and may keep running. openerGrace tells the two apart; a command
		// still running then is left to run.
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
		h.notify(h.tr("autodoc.notice.html.no_file_is_open_open_one_then_file_open_with_def"))
		return
	}
	full := h.diskPath()
	if full == "" {
		h.notify(h.tr("autodoc.notice.html.the_workspace_s_folder_is_not_known_yet_try_agai"))
		return
	}
	do(h, func(ctx context.Context) error { return h.browser(ctx, full) }, func(err error) {
		if err != nil {
			h.failed("open with the default app", err)
			return
		}
		h.say(h.trf("autodoc.notice.html.opened_with_its_default_app", h.file.path))
	})
}

// htmlPreviewFile writes content as a page in the user's cache (env's, nil for AutoDoc's own).
func htmlPreviewFile(env procenv.Env, content []byte) (string, error) {
	cache, err := userdirs.Cache(env)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(cache, "autodoc", "previews")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(directory, fmt.Sprintf("%x.html", sha256.Sum256(content)))
	if err := localfs.WriteFile(context.Background(), path, content); err != nil {
		return "", err
	}
	return path, nil
}

// previewHTML is File › Preview HTML: the note's page in the pane beside the editor (htmlpane.go),
// drawn natively in a window and in cells in a terminal, where it needs neither a graphics protocol
// nor a browser on the host (an ssh session's).
func (h *Host) previewHTML() { h.openHTMLPane() }
