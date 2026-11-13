//go:build !unix

package app

import (
	"errors"
)

var errNoBridge = errors.New("a crew bridge (agy, Codex) runs only on unix")

func detachBridge(string, string, string, []string, []string) (int, string, error) {
	return 0, "", errNoBridge
}
