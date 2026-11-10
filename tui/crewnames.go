package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/golib/paths"

	"github.com/yongjohnlee80/autodoc/internal/filelock"
)

// CREW NAMES ON DISK (docs/decisions/crew.md#crew-names-on-disk): a crew identity from data, a scope or
// a codename, becomes a file or folder name only through crewFileName, the same on every OS. Builds
// before this one wrote a scope as it is (autodoc:1f2e…), which Windows cannot hold;
// migrateCrewNames renames those once, before anything reads the folders.

// crewFileName is identity as one path element: paths.Encode's, so a colon, a separator or a name
// Windows reserves is escaped, and two identities never share a name.
func crewFileName(identity string) string { return paths.Encode(identity) }

// crewNamesLock is the lock migrateCrewNames holds in base, so two windows starting together rename
// once between them.
const crewNamesLock = ".crew-names.lock"

// migrateCrewNames renames, under base, what an earlier build named by a scope's own spelling to
// crewFileName's: .crew-scopes/<scope>, .crew-todos/<scope>, and each workspace's <scope>/ folder of
// agents. Only a name holding a colon is a scope's old name: every scope has one (autodoc:, autovim:,
// nvim:), and nothing else AutoDoc writes there does.
//
//   - Each rename is one rename in its own folder: a run cut short leaves each name old or new,
//     never half, and the next run renames the rest. Running it again changes nothing.
//   - A name whose new form is already there is left as it is, and so is the new one: nothing is
//     overwritten. kept names each such old path, for the user to look at.
//   - It is one way: nothing writes an old name any more.
func migrateCrewNames(base string) (kept []string, err error) {
	if _, err := os.Stat(base); errors.Is(err, fs.ErrNotExist) {
		return nil, nil // nothing written yet, so nothing to rename
	}
	unlock, err := filelock.Lock(filepath.Join(base, crewNamesLock))
	if err != nil {
		return nil, fmt.Errorf("renaming the crew's folders: %w", err)
	}
	defer func() { _ = unlock() }()
	var errs []error
	rename := func(dir string, dirs bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.Contains(name, ":") || e.IsDir() != dirs || e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			from, to := filepath.Join(dir, name), filepath.Join(dir, crewFileName(name))
			switch _, err := os.Lstat(to); {
			case err == nil:
				kept = append(kept, from)
			case !errors.Is(err, fs.ErrNotExist):
				errs = append(errs, err)
			default:
				if err := os.Rename(from, to); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	rename(filepath.Join(base, ".crew-scopes"), false)
	rename(filepath.Join(base, ".crew-todos"), false)
	workspaces, err := os.ReadDir(base)
	if err != nil {
		errs = append(errs, err)
	}
	for _, ws := range workspaces {
		if ws.IsDir() && !strings.HasPrefix(ws.Name(), ".") {
			rename(filepath.Join(base, ws.Name()), true)
		}
	}
	return kept, errors.Join(errs...)
}
