//go:build !unix

package app

import "errors"

var errNoBridge = errors.New("the agy bridge runs only on unix")

func detachBridge(string, string, []string, []string) (int, string, error) { return 0, "", errNoBridge }

func lockBridge(string, string) (func(), error) { return nil, errNoBridge }
