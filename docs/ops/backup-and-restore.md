# Backing up and restoring the store

AutoDoc keeps everything that is not a file in your workspaces in one SQLite
store, in its data directory (`server.data_dir`, else `$XDG_DATA_HOME/autodoc/`,
else `~/.local/share/autodoc/`). This page says what to back up, how to restore
it, and how to check it. A PostgreSQL store (the server component) is its
database server's to back up and check: use that server's own tools
(`pg_dump`, `pg_amcheck`).

## What to back up

Two files, and both matter:

| File | What it holds |
|---|---|
| `autodoc.db` | Workspaces and their patterns, preferences, the todo stores, the crew's sessions and mail, the embedding providers and rankers, and every workspace's index. |
| `autodoc.db.key` | The 256-bit key (mode `0600`) that seals the secrets in the store: workspace database connections' DSNs, and the embedding providers' and rankers' API keys. |

`autodoc.db-wal` and `autodoc.db-shm` are SQLite's write-ahead log and its
index. Never copy `autodoc.db` alone while a daemon serves it: the newest
writes are in the log, and a copy taken mid-write is not a database. Take a
consistent copy one of these ways:

```sh
# with the daemon running: SQLite's own online backup, consistent at one moment
sqlite3 ~/.local/share/autodoc/autodoc.db ".backup '/backups/autodoc.db'"

# or, with the daemon stopped (autodoc --call sys.shutdown), copy the file
cp ~/.local/share/autodoc/autodoc.db /backups/autodoc.db
```

Then copy the key on its own, and keep it apart from the database backup, as
you would a password. Anyone with both can read every sealed secret.

```sh
install -m 0600 ~/.local/share/autodoc/autodoc.db.key /secure/autodoc.db.key
```

## A backup without its key

The store opens without its key, and everything in it is there: workspaces,
todos, crew and mail, preferences and the index. The sealed secrets are not
readable:

- each workspace database connection must be configured again (its DSN);
- each embedding provider's and ranker's API key must be typed again.

`autodoc --check-store` lists every secret the key does not open, by what it
is (the workspace and its connection's role, the provider or the ranker), and
exits 1 while any is left. The daemon still starts: workspace settings list
such a connection as "sealed under another key: enter it again", and the rest
as they are. To re-enter them:

- a connection: in the workspace's settings, enter its DSN again (the source
  database's, or the destination's), or with `workspace.configure`;
- an embedding provider or a ranker: System › Search models… (`SPC s`), type
  its API key again.

The daemon makes a new key the first time it seals a secret; the old secrets
stay unreadable until each is entered again.

## Restoring

1. Stop the daemon (`autodoc --call sys.shutdown`), and every AutoDoc window
   with it.
2. Move the damaged or unwanted store aside: `autodoc.db`, `autodoc.db-wal`,
   `autodoc.db-shm` (keep them until the restore is checked). Leave
   `autodoc.db.damaged` where it is, if it is there: the check below clears it.
3. Copy the backup in as `autodoc.db`, and the key as `autodoc.db.key`, mode
   `0600` (a key others can read is refused).
4. Check it: `autodoc --check-store` (below). With the file found sound, it also
   clears the damage mark (`autodoc.db.damaged`), which refuses every start
   until then, even when secrets it lists still need entering again.
5. Start AutoDoc. The index is the backup's: files changed since are indexed
   again as the daemon finds them.

A backup from an older AutoDoc is brought up to date when the daemon opens it:
its schema scripts ([schema-scripts.md](schema-scripts.md)) run after the store
is checked whole, never on a damaged one. A backup from a newer AutoDoc is
refused by an older one (`ErrDowngrade`): restore it with the AutoDoc it came
from, or a newer one. `--check-store` on a store older than the binary checks
its file whole, but its tables may predate the columns the secrets check reads:
it then reports that the sealed secrets were not checked, and says why; start
the daemon once (which brings the schema up to date), then check again.

## Checking the store

```sh
autodoc --check-store
```

It prints a JSON report and exits 1 when it finds a problem:

- `integrity`: SQLite's `integrity_check`, every page, record and index;
- `foreign_keys`: `foreign_key_check`, rows whose parent row is gone;
- `secrets`: every sealed secret the store's key does not open, or every one
  when the key file is gone. The check never makes a key.

With a daemon serving the store, that daemon runs the check (the read-only
`sys.check` verb), and you need not stop anything. With none, `--check-store`
opens the store to check it: it runs no schema script and writes nothing, so
a damaged store is reported on as it is.

### The check at every start

Each time the daemon opens the store it runs SQLite's `quick_check`, which
reads every page and record (but does not match each index to its table, as
`integrity_check` does). It reads the whole file, about 0.9 s per GB with the
file in the page cache and several times that from a cold disk, so the start
runs it for at most a second:

- A store that is checked within the second is verified before anything is
  written to it.
- A larger store is served at once, and the daemon finishes the check on a
  reader in the background. If it finds damage, the daemon refuses every write
  at once (`rpc.store_damaged`), logs it, records a `store.damaged` event,
  marks the store (`autodoc.db.damaged` beside it) and stops. A large store
  closed cleanly therefore takes writes for up to about 1 s per GB before it
  is verified.
- The check runs whole, however long it takes, where a write could otherwise
  land on a damaged store first:
  - schema scripts are pending (a new AutoDoc's first start on the store);
  - `autodoc.db-wal` is there, and not empty, before the daemon opens the
    store. A clean close removes it, so it is the sign of a crash. (A `sqlite3`
    shell holding the store open leaves it too; that costs only the whole
    check. An empty one, which a checkpoint or a read-only open can leave, is
    no sign of anything);
  - the store is marked damaged (below).

### The damage mark

When the background check finds damage, the daemon writes `autodoc.db.damaged`
beside the store: the check's report, as JSON (mode `0600`). It is written only
then. From then on every start refuses the store, with that report, even when
the start's own quick check finds nothing: a quick check is not enough to clear
it. Only `autodoc --check-store` clears it, when its whole check finds the file
sound (`integrity_check` and `foreign_key_check` pass), typically after you
restore it from a backup. Secrets the key does not open do not keep the mark:
they are not damage to the file, and are entered again once the daemon serves
(above). Delete the file yourself only if you are sure the store is sound.

A store that fails the check is not served, since writing on a damaged store
can spread the damage. The daemon says so and names `--check-store` and this
page. A busy, locked or unreadable store is reported as the error it is, never
as damage.

If the store is damaged, restore it from a backup as above. With no backup,
move the damaged store aside and start AutoDoc on a new one: add the
workspaces again, and their index (documents, chunks, links and vectors) is
built again from their files. What lived only in the store is lost: the
workspaces' settings and connections, the todo stores' settings, the crew's
sessions and mail, preferences, and the providers and rankers.
