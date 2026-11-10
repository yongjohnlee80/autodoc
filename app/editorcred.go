package app

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yongjohnlee80/autodoc/internal/userperm"
)

// maxEditorCredential is the most a token file holds: a token is far shorter.
const maxEditorCredential = 256

// credentialOpened, a test's, runs between the checked open and the read: what the path names then
// is never what is read.
var credentialOpened = func() {}

// readEditorCredential is the editor token in the file at path ($AUTODOC_EDITOR_CREDENTIAL_FILE: a
// Codex agent's, whose tool shells drop $AUTODOC_EDITOR_TOKEN): a regular file, never a link, private
// to this user by the OS's rules (userperm: 0600 on Unix, an access list on Windows), holding one
// token. The checks are of the file opened, and the token is read through that handle, so a file
// put in the path's place meanwhile is never read; a FIFO is refused rather than waited on.
func readEditorCredential(path string) (string, error) {
	f, err := userperm.OpenPrivate(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	credentialOpened()
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
