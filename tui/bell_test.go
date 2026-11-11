package tui

import (
	"errors"
	"slices"
	"testing"
)

// TestTheBellPlayerFallsBack: the window's sound is canberra-gtk-play's by the sound's name, else
// pw-play's or paplay's on the sound's file when it is there, else none.
func TestTheBellPlayerFallsBack(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			if slices.Contains(names, n) {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		}
	}
	yes, no := func(string) bool { return true }, func(string) bool { return false }
	for name, c := range map[string]struct {
		look   func(string) (string, error)
		exists func(string) bool
		want   string
	}{
		"canberra first":         {has("canberra-gtk-play", "pw-play", "paplay"), yes, "/usr/bin/canberra-gtk-play"},
		"pw-play next":           {has("pw-play", "paplay"), yes, "/usr/bin/pw-play"},
		"paplay last":            {has("paplay"), yes, "/usr/bin/paplay"},
		"no sound file":          {has("pw-play", "paplay"), no, ""},
		"canberra without files": {has("canberra-gtk-play"), no, "/usr/bin/canberra-gtk-play"},
		"none":                   {has(), yes, ""},
	} {
		path, args, ok := bellPlayer(c.look, c.exists)
		if path != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q %v %v, want %q", name, path, args, ok, c.want)
		}
	}
}
