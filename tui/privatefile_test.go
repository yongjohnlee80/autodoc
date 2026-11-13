package tui

import "github.com/yongjohnlee80/autodoc/internal/userperm"

// privateFile is nil when the file at path is private to this user by the OS's rules (userperm):
// 0600 on Unix, an access list granting only this user on Windows.
func privateFile(path string) error {
	f, err := userperm.OpenPrivate(path)
	if err == nil {
		err = f.Close()
	}
	return err
}
