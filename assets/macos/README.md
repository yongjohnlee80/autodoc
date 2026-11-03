# AutoDoc.app: opening the window from Spotlight on macOS

`autodoc --gui` is a command, so macOS does not list it as an app: Spotlight (Cmd+Space),
Launchpad and the Dock cannot open it. This folder holds an app bundle template that runs it.
`install-app.sh` builds the same `AutoDoc.app` from it on every Mac, the icon included.

## Install

You need the GUI build of `autodoc` on your `PATH` (README: Quick start): Homebrew's, a release
archive's, or `go install -tags gui`. From a checkout of this repository:

```sh
assets/macos/install-app.sh                 # to ~/Applications
assets/macos/install-app.sh /Applications   # for every user (may need sudo)
```

Then press Cmd+Space, type `AutoDoc` and press Return. To keep it in the Dock, right-click its
Dock icon and choose **Options › Keep in Dock**.

The script checks that the `autodoc` it found has the window, and refuses a terminal-only build.
To run another `autodoc` than the one on `PATH`, name it:

```sh
AUTODOC_BIN=~/.local/bin/autodoc assets/macos/install-app.sh
```

Run the script again after you move `autodoc` or change the template; it replaces the app it
installed. Upgrading `autodoc` needs no reinstall: the app runs the path it was given, and
Homebrew's `/opt/homebrew/bin/autodoc` stays put across `brew upgrade`.

## What it builds

```
AutoDoc.app/Contents/
├── Info.plist                 # from the template: name, bundle id, icon
├── MacOS/
│   ├── AutoDoc                # from the template: the launcher (zsh)
│   └── autodoc-bin            # made by the script: a symlink to your autodoc
└── Resources/
    └── AppIcon.icns           # copied from assets/icon/
```

- **The launcher** takes the `PATH` an interactive login shell builds before it starts AutoDoc.
  Finder starts apps with a bare `PATH` (`/usr/bin:/bin:/usr/sbin:/sbin`) that lacks Homebrew,
  mise, nvm and `~/.local/bin`, so the agents AutoDoc starts would not find `claude`, `codex`,
  `git` or `nvim`. Reading the shell's `PATH` adds about a second to each start.
- **`autodoc-bin`** runs `autodoc` from inside the bundle, so macOS counts its window as
  AutoDoc.app: the app's name and icon in the Dock and the app switcher. Without it the launcher
  runs the `autodoc` on `PATH`, and the window shows as a bare `autodoc`.
- **The bundle id** is `io.github.yongjohnlee80.autodoc`. Quitting every AutoDoc.app window from a
  script uses it: `osascript -e 'quit app id "io.github.yongjohnlee80.autodoc"'`.

## The icon

`assets/icon/make-icon.swift` draws the icon, and `assets/icon/make-icon.sh` renders
`AppIcon.png` (1024×1024) and `AppIcon.icns` from it. Both are checked in, so installing does not
redraw them. After changing the drawing, run `make-icon.sh`, commit the two images, and run
`install-app.sh` again.

## Troubleshooting

- **Spotlight does not find it.** Spotlight indexes `~/Applications` and `/Applications`.
  `mdfind 'kMDItemFSName == "AutoDoc.app"'` shows whether it is indexed; `mdimport` on the app
  indexes it now.
- **The Dock shows the old icon.** macOS caches icons: `killall Dock` redraws it.
- **Agents cannot find a tool.** The launcher's `PATH` is the one `zsh -lic` builds. Put the
  tool's folder on `PATH` in `~/.zprofile` or `~/.zshrc`, then quit and reopen the app.
- **Each Spotlight open starts another window.** Each launch is another `autodoc --gui`, as it is
  from a terminal; every window shares the one daemon.

## Uninstall

```sh
rm -rf ~/Applications/AutoDoc.app
```
