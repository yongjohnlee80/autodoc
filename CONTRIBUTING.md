# Contributing to AutoDoc

Thank you for helping. This guide covers how to report a problem, set up a checkout, and send a
change. The rules a change must follow are in [CONVENTIONS.md](CONVENTIONS.md), and how AutoDoc is
put together is in [docs/architecture.md](docs/architecture.md). Read both before a code change.

## Ways to contribute

- **Report a bug** or a confusing behaviour (below).
- **Suggest a feature** in an issue. Say what you are trying to do, not only the change you want.
  Larger changes are agreed in an issue before any code.
- **Improve the docs**: the reference book (`docs/reference`), the tutorials (`docs/tutorials`, in
  six languages), the Lua and plugin guides.
- **Translate**: AutoDoc ships in English, Spanish, Japanese, Korean, Brazilian Portuguese and
  Simplified Chinese (`tui/qml/i18n`). Corrections from native speakers are especially welcome.
- **Write a plugin, a Lua package or a theme** ([docs/plugins.md](docs/plugins.md),
  [docs/lua](docs/lua/README.md), [docs/themes.md](docs/themes.md)).
- **Fix an issue.** Comment on it first so two people don't work on the same thing.

Be kind and assume good intent, in issues, reviews and commits.

## Reporting a bug

Search the open issues first. A useful report has:

- the AutoDoc version (`autodoc --version`), the build (TUI, `--gui`, or the server), and your OS;
- what you did, step by step, what you expected, and what happened;
- the smallest workspace or file that shows it, if one is needed;
- the log or the error AutoDoc showed. Remove anything private first.

Problems with typing, input methods or keys have their own guide:
[docs/filing-input-and-ime-bugs.md](docs/filing-input-and-ime-bugs.md).

**Security issues are not reported in public issues.** Use "Report a vulnerability" on the
repository's Security tab, which reaches the maintainer privately.

## Setting up

You need Go 1.26.9 (the version in `go.mod`) and `make`.

```sh
git clone https://github.com/yongjohnlee80/autodoc && cd autodoc
make build        # bin/autodoc: the daemon and the TUI, no cgo
make build-gui    # adds the GUI (--gui); needs cgo and the window system's libraries
```

- `make build-gui` names any missing library and the packages that provide it, for Debian/Ubuntu and
  Arch. On macOS, `make build` already includes the GUI.
- `make build-server` builds the enterprise search server (`-tags server`).

## Running the checks

Run these before you push. CI runs the same and more.

```sh
make fmt          # gofmt: the tree must be formatted
make vet          # go vet, untagged and with -tags server
make test         # the tests, with -tags server
make race         # the tests under -race, twice
go vet -tags gui ./tui/ ./app/
go test -tags gui ./tui/
make test-lua     # autodoc.nvim's Lua suites (see below)
```

- **Never run `go test -tags gui ./app/`**: it opens a window.
- The Postgres cells need `AUTODOC_TEST_PGURL`, a PostgreSQL with pgvector whose user may create
  databases; without it they skip ([core/pgstore](core/pgstore/README.md#testing)).
- **The Lua suites** (`make test-lua`, which runs `tests/run-all.sh`) need:
  - Neovim on `PATH` (CI uses v0.12.2);
  - a checkout of [auto-core.nvim](https://github.com/yongjohnlee80/auto-core.nvim), named by
    `AUTODOC_TEST_AUTOCORE` or found beside this checkout. The run stops if there is none. CI pins
    the revision it tests against (`AUTO_CORE_SHA` in `.github/workflows/ci.yml`); check out the
    same one:

    ```sh
    sha=$(sed -n 's/^ *AUTO_CORE_SHA: *"\([0-9a-f]*\)".*/\1/p' .github/workflows/ci.yml)
    git clone https://github.com/yongjohnlee80/auto-core.nvim ../auto-core.nvim
    git -C ../auto-core.nvim checkout "$sha"
    make test-lua
    ```

  - The composition suite, the drawer hosted by the real auto-finder.nvim, is optional. It runs
    when `AUTODOC_TEST_AUTOFINDER` names a checkout of it, or one is found beside this checkout,
    and is reported skipped otherwise.
- The TUI tests are long. `go test ./tui -run <Name>` runs one; CI runs them in four shards
  (`tests/run-tui-shard.sh`).

### Generated files

Several files are generated, and a test fails when one falls behind. Regenerate rather than edit by
hand, and include the result in your commit:

| After changing… | Run |
| --- | --- |
| a key (`tui/keys.go`) | `go test ./tui -run TestKeysAreGenerated -update` |
| the Go tables behind catalog ids | `go test -tags gui ./tui -run TestTheEnglishCatalogHoldsTheGoTables -update` |
| a verb's contract | `go test ./rpc -update`, then `go generate ./rpc` |
| the Lua surface | `go generate ./tui` |
| an index golden | `go test ./core/index -update-goldens` |

[CONVENTIONS.md §10](CONVENTIONS.md#10-what-moves-with-a-change) lists everything else a change must
update alongside its code: the help, all six language catalogs, the reference book, and so on.

## Sending a change

1. **Fork** the repository and create a branch from `main`, named for the change
   (`fix/find-chip-overlap`, `feat/layout-menu`).
2. **Keep it focused.** One pull request per change. Inside it, one commit per concern: a refactor,
   a fix and a feature are separate commits. See [CONVENTIONS.md §11](CONVENTIONS.md#11-commits) for
   the commit message form.
3. **Add a test that fails without your change**, and check that it does.
4. **Run the checks** above.
5. **Open the pull request** against `main`. Describe:
   - what changes for the user, and why;
   - how you tested it;
   - anything a reviewer should look at closely.

   Open it early as a draft if you want feedback on the approach before the details.

### What CI checks

Every pull request runs:

- `gofmt`, `go vet` (untagged, `-tags server`, `-tags gui`) and the builds;
- the tests under `-race`, twice, against a pgvector service;
- the TUI tests under `-race`, in four shards;
- the Lua suites in Neovim, against the pinned auto-core.nvim;
- on Linux, the GUI build and its TUI tests (`-tags gui`);
- on macOS, the builds with and without the GUI for both architectures, and the core and RPC tests
  under `-race` (FSEvents through cgo);
- the untagged build reaching no server code, and no private module in the dependency graph;
- **the coverage policy**: changed code at least 70% covered, and no file and no total dropping more
  than 1 point. The report is in the job's summary. A pull request from a branch of this repository
  also gets it as a comment; one from a fork does not, since its token is read-only.

A pull request is merged once CI is green and a maintainer has approved it. Review comments are
answered with new commits on the same branch.

### Review

Reviewers check a change against [CONVENTIONS.md](CONVENTIONS.md). Expect questions about:

- **where the change lives** (the right package, no second copy of a rule);
- **whether it bends an existing behaviour** to fit;
- **the test** that proves it;
- **the docs and translations** that move with it.

It isn't personal; it keeps AutoDoc consistent for everyone who comes after.

## Releases

Maintainers tag releases as `vMAJOR.MINOR.PATCH`. A tag builds the binaries for Linux and macOS and
publishes the docs site; the release notes are generated from the merged pull requests.

## License

AutoDoc is licensed under the [Apache License 2.0](LICENSE). By sending a contribution you agree
that it is licensed under the same terms (section 5 of the license). There is no separate
contributor agreement.
