//go:build !unix

package app

import "errors"

// readEditorCredential: the token file is a Unix agent's (editorcred_unix.go).
func readEditorCredential(string) (string, error) {
	return "", errors.New("a token file is read on Unix only")
}
