# AutoDoc conventions

The rules every change to AutoDoc follows. They are strict: a change that breaks one is not merged,
however small. Most are enforced by a test or a CI step, named beside the rule; the rest are held in
review.

This file is the one place the rules are stated. [docs/architecture.md](docs/architecture.md)
explains how AutoDoc is put together and why, with examples, and links here for the rules: read it
before adding a feature. Where the two seem to disagree, this file holds, and the disagreement is a
docs bug to report. [CONTRIBUTING.md](CONTRIBUTING.md) covers how to send a change.

## 1. Principles

These come before every rule below. When no rule covers a case, they decide it.

- **Single responsibility.** A package is one concept; a type has one job. A change that makes a type
  do two things splits it.
- **Open for extension, closed for modification.** Add a capability beside the existing ones (an
  option, an interface a type may implement, a new verb) rather than change what callers already
  rely on. A shared type is not bent to serve one caller.
- **Interfaces are the consumer's.** A port is declared by the package that needs it, and is as small
  as that need ([architecture §3](docs/architecture.md#3-dependency-rules)).
- **Depend on abstractions at the seams.** Core takes its disk, clock and logger as dependencies
  (golib `vfs`, `WithClock`, `WithLogger`), never globals.
- **DRY, strictly.** One rule lives in one place. A second copy of a rule, a switch, a parser or a
  path to the same state is a defect, even when it is "only" in a test helper or a QML file. Reuse
  first: an existing AutoDoc helper, then golib, then new code.
- **Fix at the source.** A bug is fixed where it arises, not where it shows. No workaround in a
  caller for a defect in a callee, no retry loop around a race, no sleep around an ordering problem.
- **No escape logic.** A rule with "except when…" (a flag to skip a step, a special case for one
  widget, a state tracked only to undo another rule) means the design is wrong. Fix the design.
- **Never bend a widget's workflow.** A widget keeps its own behaviour (focus, raise, keys, close).
  A container may constrain where and how large a child is; it does not override how the child
  behaves.
- **Additive by default.** Public APIs, the wire protocol, the plugin protocol and released
  migrations change by addition. A removal or a change of meaning is a deliberate, versioned step
  (§4, §7, §8).

## 2. Packages and dependencies

The packages and their jobs are in [architecture §2](docs/architecture.md#2-packages) and the
[README](README.md). The rules:

| Rule | Enforced by |
| --- | --- |
| `core/*` never imports `rpc`, `app`, `tui` or `internal`. `core/module` imports nothing of AutoDoc's | review; see the targeted guards below |
| `script` imports nothing of AutoDoc's (golib and gopher-lua only) | `script/conformance_test.go` `TestScriptImportsNothingOfAutoDocs` |
| `core/index` and `core/pgstore` never import each other | `core/pgstore/pgwriter/pgwriter_test.go` `TestNeitherIndexNorPgstoreImportsTheOther` |
| `core/todo` and `core/store` never import each other; `core/todo/sqlitejournal` joins them | `core/todo/sqlitejournal/deps_test.go` |
| `core/workspace` imports no config | `core/workspace/deps_test.go` `TestWorkspaceImportsNoConfig` |
| `rpc` is a projection of core: it adapts contracts to the wire and holds no business logic | review |
| The untagged build reaches no Postgres, no `enterprise` and no `core/pgstore` | CI step "the untagged build has no server"; `app` `TestAnUntaggedBuildRefusesTheServer` |
| No private module appears in `go.mod`, `go.sum` or the test dependency graph | CI step "no private module" |
| Shared UI, parsing, storage, search and RPC primitives come from golib. A primitive another golib consumer could use is added to golib, not to AutoDoc | review |
| A new dependency outside golib and the standard library is agreed in an issue first | review |

## 3. Core packages

| Rule | Enforced by |
| --- | --- |
| A package has a `doc.go` (what it is, its concurrency claim) and a `README.md` | review |
| Constructors are `New(required…, opts ...Option)`; `ctx` comes first on any I/O | review |
| **No direct I/O in new `core/` code.** A filesystem operation goes through golib `vfs`. Known deviations remain: `core/store`'s SQLite plumbing (the lease, the key slot) and `core/export`'s site and embed reading the local disk. A change does not extend them, and moves one when it touches it | review |
| **Data access goes through `core/store`** (or `core/pgstore`). Nowhere else imports `golib/dao/sqlite` or runs SQL | `core/store/source_test.go` `TestNoDataAccessOutsideTheStore` |
| A table a workspace owns has a scope accessor, and every accessor and join stays inside its workspace | `core/index/tables_test.go`, `core/store/store_test.go` |
| Time comes from the injected clock: no `time.Now()` in logic | review |
| **No `time.Sleep` in production code.** Wait on the event (a channel, a context) or a cancellable timer. Every timed wait has a `NOTE:` comment directly above it saying why it must be timed | `core/clock/waits_test.go` `TestProductionCodeSaysWhyItWaits` |
| Logging goes through `WithLogger(logger.Logger)` | review |
| Errors are `errs.Sentinel(kind, "pkg: message")`, wrapped with `%w`. A public one is listed in its module's `Errors()` | review; `rpc` `TestWireErrMapsEverySentinel` |
| Wire types are json-tagged structs in their package | review |

## 4. Daemon features and the RPC protocol

A daemon feature is a core package plus a `Module` ([architecture §4.1](docs/architecture.md#41-a-daemon-feature-is-a-core-package-plus-a-module)).

- **A verb is its contract and its handler**, through `module.Handle`. Parameters come from the
  handler's struct: field order is position, the `param:"name"` tag is the name.
- **`Name`, `Verbs` and `Errors` do no I/O**, so `--contracts` builds every module with no
  dependencies.
- **Registration is one list:** `app/serve.go`'s `modules(deps)`. No other registration path.
- **Error codes:** a module's codes sit in its own range, and −32060 to −32070 are the shared codes
  any module may answer. A code is chosen by what the client does next, and is documented with its
  name and its next step. Enforced by `core/module` `CheckCodes` at registration, and `rpc`
  `TestEveryCodeIsDocumented`.
- **Only the mapped message crosses the wire**, never an error's own text.
- **Protocol (`rpc.Protocol`, `rpc.MinProtocol`):**
  - any change to verbs, parameters or results raises `Protocol`, and the comment above it records
    what changed;
  - a session keeps the protocol it declared; a result may gain keys, never lose or change one;
  - a change that is not an addition raises `MinProtocol`, or keeps the old behaviour for sessions
    below it;
  - `sys.hello` and `sys.shutdown` never change.
- **Generated outputs follow the contracts**, and tests fail until they do:
  - `go test ./rpc -update` rewrites `rpc/testdata/contracts.json` and the wire goldens;
  - `go generate ./rpc` rewrites AGENTS.md's tables and `lua/autodoc/contracts.lua`.
  - Enforced by `rpc` `TestContractsAreGolden`, `TestWireGoldens`, and `internal/contractsgen`
    `TestAgentsMDIsGenerated`, `TestLuaContractsAreGenerated`.
- **The daemon never pushes.** Clients poll `sys.events`.

## 5. The TUI and GUI

A UI feature is a `Feature` ([architecture §4.2](docs/architecture.md#42-a-ui-feature-is-a-feature)).

- **A feature sees `Core`, never `Host`.** A panel gets its scoped `PanelHost`. New code does not
  reach past them.
- **The screen is QML.** A feature's QML is a component in `tui/qml/dialogs` or `tui/qml/views`,
  declared once in `main.qml`; every QML file must load (`TestEveryQMLFileIsSound`).
- **`App.*` commands are the whole surface QML may call** (`tui/commands.go`). A user-visible action
  is also a catalog entry (`tui/builtins.go`, `tui/catalog.go`), whose `Run` calls the same function
  as its `App.*` twin. One action, one function.
  - Catalog ids are `<subject>.<action>`, lower-case and dotted. `lua.*` belongs to Lua and
    `plugin.<name>.*` to that plugin (`TestTheCatalogsNamespacesAreOwned`).
  - Every menu row and leader key is a catalog entry (`TestEveryMenuRowAndLeaderKeyIsACatalogEntry`).
- **Keys are declared once, in `tui/keys.go`.** The key blocks in `main.qml`, the card views and
  `docs/keymap.md` are generated from it: run `go test ./tui -run TestKeysAreGenerated -update`.
  Every key is in the help (`TestEveryKeyIsInTheHelp`).
- **One layout per kind of choice:** a list to filter and choose from is `Picker.qml` over a
  `pick[T]`; one choice among a few, always in view, is a `ComboBox`. No new layout for either.
- **Background work answers on the loop, and can carry its failure** (`Core.Go`, latest answer wins
  through `Ticket`). Enforced by `TestEveryBackgroundAnswerCanCarryAFailure`.
- **Build tags:** GUI-only code is in `*_gui.go` with `//go:build gui`, and its terminal counterpart
  in `*_tui.go` with `//go:build !gui`. The same pattern holds for `server` and the other optional
  parts. No runtime check stands in for a build tag.
- **Every theme keeps text legible**, and every theme golib ships is on the theme menu
  (`TestEveryThemeKeepsTextLegible`, `TestEveryThemeGolibShipsIsOnTheThemeMenu`).

## 6. User-facing text and languages

AutoDoc ships in six languages: `en`, `es`, `ja_JP`, `ko_KR`, `pt_BR` and `zh_CN`.

- **No user-facing string is written in code.** QML uses `qsTrId("autodoc.…")`; Go uses `tr`/`trf`
  (`Core.Tr`/`Trf` in a feature) or `tuicore.Msg`.
- **Ids are `autodoc.<kind>.<area>.<slug>`**, the slug taken from the English text.
- **Every id is in all six catalogs** (`tui/qml/i18n/autodoc_<lang>.xml`). Tests check the
  inventory: every id used is in the English catalog (`TestEveryIDIsInTheEnglishCatalog`), every
  shipped language covers every id (`TestEveryShippedLanguageCoversEveryID`), and a language switch
  relabels the whole screen (`TestALanguageSwitchRelabelsTheScreen`,
  `TestASwitchLeavesNoEnglishOnScreen`).
- **A translation is a translation, never copied English.** No test can tell; review holds it.
- **A sentence is translated whole and then filled** (`%1`…`%99`), never assembled from fragments.
- **Ids generated from the Go tables** (keys, cards, commands, help, choices, themes) are refreshed
  with `go test -tags gui ./tui -run TestTheEnglishCatalogHoldsTheGoTables -update`.
- **Notices and errors are different things:**
  - a user's precondition ("open the terminal first") is a translated notice, `notify(tr(…))`;
  - a genuine failure is kept, `keep(err)`, which shows the translated "something went wrong" with
    the error's text;
  - a background failure the user need not see now is recorded, `record(err)`.
- **Every card and dialog fits in every language** (`TestEveryCardFitsInEveryLanguage`,
  `TestEveryLanguageWalksTheScreen`).

## 7. Plugins and Lua

- **The plugin protocol** (`plugin.Protocol`, `plugin.MinProtocol`) rises only for something a
  manifest can declare that an older host must not run without, a changed meaning, or something a
  plugin must handle. Each version has its own `plugin/protocolN_test.go`.
- `docs/plugins.md` documents every plugin method, and every manifest sample in it decodes
  (`TestThePluginReferenceKeepsUp`).
- **Lua:** a capability a script may use is gated by its contract's effect (`tui/luagate.go`,
  `script/gate.go`). The reference's generated blocks are rewritten by `go generate ./tui`
  (`TestTheLuaReferenceIsGenerated`), and every Lua block in `docs/lua` runs (`TestTheLuaDocsRun`).

## 8. Storage and migrations

- **One SQLite file, one writer connection.** A module declares its own tables through
  `store.Declare`.
- **Migrations are numbered pairs** in `sql/deployments/sqlite/` (`0000NN_update_*.sql` with its
  `_revert_`). Their numbering and pairing load on every engine (`TestEveryEngineLoads`).
- **Every table is documented** in `docs/ops/schema-scripts.md` (`TestTheSchemaDocListsEveryTable`),
  and every script is listed there. The script list is held in review, since a migration that only
  adds a column or an index changes no table name.
- **A released migration never changes.** Its digest is pinned (`TestReleasedScriptsAreUnchanged`,
  and the same for Postgres in `core/pgstore`). A fix is a new migration.
- Every engine names the same scripts (`TestEveryEngineNamesTheSameScripts`).

## 9. Tests

- **Every behaviour change comes with a test that fails without it.** A test that passes on the old
  code proves nothing; check that it fails there before relying on it.
- **Names are sentences** stating what is true: `TestAKeyTypedUnderTheTooSmallScreenStaysThere`, not
  `TestTooSmall2`. A short comment above says what it shows.
- **The surface is tested black-box** (`<pkg>_test`); internals white-box. Test-only exports go in
  `export_test.go` only. A test hook in production code is a nil package variable, set only by tests,
  and its comment says so.
- **A contract several implementations meet gets a `<pkg>test` package** that each one runs.
- **Disks are `vfs/memfs`**; `vfs/local` over `t.TempDir()` only where the real file system is the
  point.
- **No sleeps to wait for something.** Wait on the event, the screen or the daemon's clock (golib
  `decltest`'s `WaitFor`, `WaitForText`).
- **Tests are race-clean and order-independent.** CI runs them with `-race -count=2`. A test that
  leaves a goroutine, a file descriptor or a socket behind breaks the next one; clean up in
  `t.Cleanup`, and wait for work you started before closing what it uses.
- **Goldens are rewritten by their `-update` flag** (`-update-goldens` for `core/index`), never by
  hand, and the diff is reviewed.
- **No mass moves.** An existing white-box test that uses only the exported API moves to
  `<pkg>_test` when its file is next changed for another reason, not in a sweep.
- **Never run `go test -tags gui ./app/`**: it opens a window.

## 10. What moves with a change

A change updates, in the same pull request, everything its behaviour reaches. The guards above fail
when a generated file falls behind; the rest is held in review.

| A change to… | Also updates |
| --- | --- |
| a key | `tui/keys.go`, then the generated `main.qml` blocks, card views and `docs/keymap.md`; the help |
| a command or catalog entry | `tui/builtins.go` / the feature's `Catalog()`, the help (`tui/help.go`), all six catalogs |
| any user-facing text | all six catalogs |
| a verb, its params, results or errors | the contract, `Protocol`, `contracts.json` and the wire goldens, AGENTS.md and `contracts.lua` |
| the Lua surface | `docs/lua/reference.md` (generated blocks and hand-written rows) |
| the plugin protocol | `plugin.Protocol` when §7 says so, `docs/plugins.md`, a `protocolN_test.go` |
| the schema | a new migration pair, `docs/ops/schema-scripts.md` |
| a user-visible behaviour | the relevant `docs/reference` chapter, and the tutorials it changes, in every language |
| a package's role | its `doc.go`/`README.md`, and [architecture §2](docs/architecture.md#2-packages) |

## 11. Commits

- **One concern per commit.** A refactor, a fix and a feature are separate commits, even in one pull
  request. A refactor keeps behaviour; its docs change in the same commit.
- **Subject:** `<area>: <what is now true, present tense>`, no trailing period. `area` is the
  package or part touched (`tui`, `rpc`, `core/index`, `docs`, `go.mod`), comma-joined when several.
  For example: `tui: Ctrl+Z never stops a panel's program; Resume sends a SIGCONT`.
- **Body:** why, in short paragraphs or bullets. Name what a reviewer should check.
- **Dependency raises** read `go.mod: golib vX.Y.Z and gui vA.B.C (<what it brings>)`.
- **No generated-by or AI-authorship trailers** in commits or pull requests.
