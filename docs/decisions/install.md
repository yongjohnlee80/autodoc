# Install decisions

How AutoDoc is installed and updated without a package manager: two POSIX `sh` scripts attached to
every release, `install-autodoc.sh` and `update.sh`. The developer routes (`go install`, mise, a
release tarball by hand) stay as they are; these scripts are one more route, for a user who wants
AutoDoc's window and an Update button.

## Scripts over the release's tarballs

The release already builds and checksums the GUI binaries for Linux and macOS, amd64 and arm64
(`autodoc-<v>-<os>-<arch>.tar.gz` and its `.sha256`). The installer downloads the one for this
system into a temp dir, checks its checksum and its `--version`, and installs it; CI builds nothing
new for it. It works as `curl -fsSL <url> | sh` and as a downloaded file. Native packages (deb, rpm,
a dmg, a Homebrew cask) each need their own build, signing and repository, and can come later
beside this route.

Where no prebuilt binary fits the system, the installer asks before building from the release's
source tarball (`autodoc-<v>-src.tar.gz`, checksummed like the binaries; GitHub's own source
archives aren't, as their bytes can change). It needs Go of the version `go.mod` names (a system
`go`, or mise's) and, on Linux, the window system's libraries, which it checks with `pkg-config`
and lists with the distro's install line, never installing them itself. `--build` answers yes, and
`--yes` answers every question no.

On Windows it points at `install-autodoc.ps1` and installs nothing (exit 2); see [Windows](#windows).

In the code: `scripts/install/install-autodoc.sh`, `.github/workflows/release.yml`

## A user-level install

The binary goes in `~/.local/bin/autodoc` (`--prefix` moves it), the record and update.sh in
`${XDG_STATE_HOME:-~/.local/state}/autodoc/`. Nothing needs sudo, so an update can replace the
binary from inside AutoDoc without asking for a password, and removing AutoDoc is deleting files the
user owns.

When `~/.local/bin` isn't on `PATH`, the installer prints the line to add and edits no shell rc
file: an rc edit made without asking surprises a user and breaks managed dotfiles.

In the code: `scripts/install/install-autodoc.sh`

## The install record

`install.toml` says the script installed AutoDoc, which version, and where the binary, the launcher
and update.sh are. Only the scripts write it; AutoDoc reads it and never writes it. An AutoDoc
installed another way has no record, so update.sh refuses it (exit 2) and AutoDoc offers no Update
for it: a binary another tool manages is never overwritten.

In the code: `scripts/install/install-autodoc.sh`, `scripts/install/update.sh`

## Launchers

The installer adds a launcher that runs `autodoc --gui`: on Linux a freedesktop entry in
`~/.local/share/applications/autodoc.desktop` with AutoDoc's icon, which application launchers read;
on macOS `~/Applications/AutoDoc.app`, built by the release's `install-app.sh`. A launcher that
already runs the same binary is kept as it is; one that runs another binary is replaced only when
the user agrees. `--no-launcher` adds none.

In the code: `scripts/install/install-autodoc.sh`, `assets/macos/install-app.sh`

## Nothing moves until everything is checked

Every download and its checksum land in a temp dir first. The binary is copied beside the old one
and renamed over it as the install's last step: a dropped network or a failed check leaves the old
install whole, and a running AutoDoc keeps the file it started from. Rerunning the installer
repairs or upgrades, and never duplicates a launcher.

Exit codes: 0 installed or current; 2 not supported here; 3 declined, or a build's dependency
missing; 4 a download, a checksum or the install failed, with nothing changed.

In the code: `scripts/install/install-autodoc.sh`

## update.sh runs the newest installer

update.sh doesn't carry its own copy of the install steps. When a newer release exists, it fetches
that release's update.sh (checksum-checked, into a temp dir) and runs it, so an update always runs
the newest update logic, and that one runs the release's `install-autodoc.sh --yes`: one install
path to fix and to test. update.sh never replaces itself in place; the installer installs the new
update.sh as the last step of a successful update.

Exit codes: 0 updated or current; 2 not a script install; 3 a download or a checksum failed, with
nothing changed; 4 the install failed, with the old AutoDoc untouched.

In the code: `scripts/install/update.sh`, `scripts/install/test/run.sh`

## About's Update and the restart

When the record says the script installed this AutoDoc, About offers Update, and a newer release's
notice names it as the way to update. Update runs update.sh in a tab of the terminal panel: the user
sees its progress, can answer it, and can read why it failed. When it ends well and the binary at
AutoDoc's path is newer than the one running, AutoDoc asks to restart: the backend restarts as
Restart backend… does, and once the new one answers, the UI quits and the program starts the new
binary in its place with the same arguments. Unsaved work is asked about first, as a quit asks.
Nothing is downloaded or replaced without the user's Update, and AutoDoc reads no output of
update.sh: it trusts only the new binary's `--version`.

In the code: `internal/install`, `tui/update.go`, `tui/startup.go`, `app/relaunch.go`

## Windows

On Windows, AutoDoc installs with `install-autodoc.ps1` and updates with `update.ps1`, the PowerShell forms of the two shell scripts. They run in Windows PowerShell 5.1 and in PowerShell 7, need no administrator rights, and keep the shell scripts' rules: the newest release (or `-Version`), checked against its sha256 before anything moves; the same exit codes; and `-Yes` answers every question no.

- **Where it goes, per user:**
  - the binary: `%LOCALAPPDATA%\Programs\AutoDoc\autodoc.exe` (`-Prefix` moves it);
  - the install record and `update.ps1`: `%LOCALAPPDATA%\autodoc`, Windows' counterpart of `~/.local/state/autodoc`. The record has the same keys, `update` naming `update.ps1`;
  - the launcher: a Start menu shortcut, `AutoDoc`, that runs `autodoc.exe --gui`.
- **The swap:** a running `autodoc.exe` can't be replaced, but it can be renamed. The old binary moves aside, the new one takes its name in one move, and the old one is deleted when it no longer runs: then, or at the next install.
- **The PATH:** changed only with `-AddToPath`; otherwise the script prints the command that would.
- **Unsigned:** the build is unsigned, so the first run can show Windows' unknown-publisher warning.
- **Updating:** `update.ps1` runs the newest release's `install-autodoc.ps1`, fetched and verified, so there's one install path, as on Linux and macOS.

In the code: `scripts/install/install-autodoc.ps1`, `scripts/install/update.ps1`, `scripts/install/test/run.ps1`
