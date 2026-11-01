//go:build !unix

package app

import (
	"context"
	"errors"
)

var errNoBridge = errors.New("a crew bridge (agy, Codex) runs only on unix")

func detachBridge(string, string, string, []string, []string) (int, string, error) {
	return 0, "", errNoBridge
}

func lockBridge(string, string, string) (func(), error) { return nil, errNoBridge }

// lockFolder takes no lock where flock isn't: hooks of one session may then overlap.
func lockFolder(context.Context, string) (func(), error) { return func() {}, nil }
