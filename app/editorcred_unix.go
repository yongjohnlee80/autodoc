//go:build unix

package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

// maxEditorCredential is the most a token file holds: a token is far shorter.
const maxEditorCredential = 256

// credentialOpened, a test's, runs between the open and the checks: what the path names then is
// never what is read.
var credentialOpened = func() {}

// readEditorCredential is the editor token in the file at path ($AUTODOC_EDITOR_CREDENTIAL_FILE: a
// Codex agent's, whose tool shells drop $AUTODOC_EDITOR_TOKEN): a regular file, never a link, that
// only its owner (this user) can read or write, holding one token. The checks are of the file
// opened, and the token is read from it, so a file put in the path's place meanwhile is never
// read. It is opened without blocking, so a FIFO there is refused rather than waited on.
func readEditorCredential(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ELOOP) {
		return "", errors.New("it is not a regular file (a link)")
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	credentialOpened()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", errors.New("it is not a regular file")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("others can open it (mode %o): it must be 0600", fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return "", errors.New("another user owns it")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxEditorCredential+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxEditorCredential {
		return "", fmt.Errorf("it holds more than %d bytes, not a token", maxEditorCredential)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", errors.New("it holds 0 bytes, not a token")
	}
	return tok, nil
}
