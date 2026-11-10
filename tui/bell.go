package tui

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// THE CORE'S BELL: what Core.Bell rings, for the crew's bell (crewbell.go) or any feature's. The
// terminal build rings the terminal's own bell (BEL, through golib's App.Bell); the window plays the
// desktop's message sound: canberra-gtk-play, else pw-play, else paplay.

// crewBellSound is the freedesktop message sound pw-play and paplay are given.
const crewBellSound = "/usr/share/sounds/freedesktop/stereo/message-new-instant.oga"

// crewBellWait bounds one sound's player: a stuck player must not linger.
const crewBellWait = 5 * time.Second

// ringBell is Core.Bell: the terminal's bell in the terminal build, the desktop's message sound in
// the window, played off the loop.
func (h *Host) ringBell() {
	if !h.gui {
		if h.p != nil {
			h.p.App().Bell()
		}
		return
	}
	path, args, ok := bellPlayer(func(name string) (string, error) { return procenv.LookPath(h.seams.env, name) }, fileExists)
	if !ok {
		return
	}
	do(h, func(ctx context.Context) any {
		ctx, cancel := context.WithTimeout(ctx, crewBellWait)
		defer cancel()
		return exec.CommandContext(ctx, path, args...).Run() // a sound not played is no failure to report
	}, func(any) {})
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// bellPlayer is the program that plays the message sound, and its arguments: canberra-gtk-play by
// the sound's name, else pw-play or paplay on its file when the file is there; ok is false when
// there is none.
func bellPlayer(look func(string) (string, error), exists func(string) bool) (path string, args []string, ok bool) {
	if p, err := look("canberra-gtk-play"); err == nil {
		return p, []string{"-i", "message-new-instant"}, true
	}
	if !exists(crewBellSound) {
		return "", nil, false
	}
	for _, name := range []string{"pw-play", "paplay"} {
		if p, err := look(name); err == nil {
			return p, []string{crewBellSound}, true
		}
	}
	return "", nil, false
}
