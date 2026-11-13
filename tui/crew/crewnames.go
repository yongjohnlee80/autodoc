package crew

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	corepaths "github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/internal/filelock"
	"github.com/yongjohnlee80/autodoc/internal/localfs"
	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// CREW NAMES ON DISK (docs/decisions/crew.md#crew-names-on-disk): a crew identity from data, a scope or
// a codename, becomes a file or folder name only through CrewFileName (core/paths.Name), per OS: a
// scope's ':' as it is on Linux and macOS, escaped on Windows, and no '%' anywhere. Builds before
// this one wrote other forms: a scope as it is (autodoc:1f2e…), and then every escape as %XX
// (autodoc%3A1f2e…), which a program that percent-decodes its working directory cannot find.
// migrateCrewNames renames both once, before anything reads the folders.

// crewNamesLock is the lock migrateCrewNames holds in base, so two windows starting together rename
// once between them.
const crewNamesLock = ".crew-names.lock"

// agentsOwnEntries are the agents' folder's own entries, beside its workspaces' folders: the crew's
// scope and todo-feed files, the fallbacks, the agents' PATH links and the rename's lock. Every other
// folder there is a workspace's, a dot-named one (.kb) too.
var agentsOwnEntries = map[string]bool{".crew-scopes": true, ".crew-todos": true, ".crew-fallbacks": true, ".bin": true, crewNamesLock: true}

// migrateCrewNames renames, under base, what an earlier build named otherwise to CrewFileName's:
// .crew-scopes/<scope>, .crew-todos/<scope>, and each workspace's <scope>/ folder of agents. An
// earlier name is one in golib paths.Encode's %XX form (core/paths.ParseOldName), or one holding a
// colon, a scope's own spelling (every scope has one: autodoc:, autovim:, nvim:); a name already in
// CrewFileName's form is left as it is, and so is any other.
//
//   - Each rename is one rename in its own folder: a run cut short leaves each name old or new,
//     never half, and the next run renames the rest. Running it again changes nothing.
//   - A name whose new form is already there is left as it is, and so is the new one: nothing is
//     overwritten. kept names each such old path, for the user to look at.
//   - A folder an agent still works in (another window's, still running) is renamed as any other on
//     Linux and macOS, and a relative link left at its old name (localfs.LinkOldName): that window
//     names the folder by the old path in its agents' environment ($AUTODOC_CREW_CONTEXT) and
//     writes into it, and both reach the folder through the link. The link is a symlink, which the
//     next run skips, so nothing renames twice. Windows refuses to rename a folder in use, which is
//     reported and tried again at the next start; one renamed there was held by nothing, and gets
//     no link.
//   - Only a folder gets a link: a scope's claim and its read todo stores are files a window reads
//     by listing, never by a path it kept.
//   - It is one way: nothing writes an earlier name any more.
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
		entries, err := localfs.ReadDirIfAny(dir) // no folder: nothing to rename; a file there: an error, on every OS
		if err != nil {
			errs = append(errs, err)
			return
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() != dirs || e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			id, ok := earlierName(name)
			if !ok {
				continue
			}
			from, to := filepath.Join(dir, name), filepath.Join(dir, agents.CrewFileName(id))
			switch _, err := os.Lstat(to); {
			case err == nil:
				kept = append(kept, from)
			case !errors.Is(err, fs.ErrNotExist):
				errs = append(errs, err)
			default:
				if err := os.Rename(from, to); err != nil {
					errs = append(errs, err)
				} else if dirs {
					// a window started before this one still names the agents' folder by its old
					// name ($AUTODOC_CREW_CONTEXT): the link keeps that name reaching it
					if err := localfs.LinkOldName(from, to); err != nil {
						errs = append(errs, err)
					}
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
		if ws.IsDir() && !agentsOwnEntries[ws.Name()] {
			rename(filepath.Join(base, ws.Name()), true)
		}
	}
	return kept, errors.Join(errs...)
}

// earlierName is the identity an earlier build wrote as name, when it is one CrewFileName now writes
// otherwise: ok is false for a name already in CrewFileName's form, and for any other name.
func earlierName(name string) (id string, ok bool) {
	if _, err := agents.ParseCrewFileName(name); err == nil {
		return "", false
	}
	if id, ok := corepaths.ParseOldName(name); ok {
		return id, agents.CrewFileName(id) != name
	}
	if strings.Contains(name, ":") {
		return name, agents.CrewFileName(name) != name
	}
	return "", false
}
