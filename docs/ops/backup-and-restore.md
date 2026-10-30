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
is. The daemon makes a new key the first time it seals a secret, so set the
secrets again after a restore without the key; the old ones stay unreadable.

## Restoring

1. Stop the daemon (`autodoc --call sys.shutdown`), and every AutoDoc window
   with it.
2. Move the damaged or unwanted store aside: `autodoc.db`, `autodoc.db-wal`,
   `autodoc.db-shm` (keep them until the restore is checked).
3. Copy the backup in as `autodoc.db`, and the key as `autodoc.db.key`, mode
   `0600` (a key others can read is refused).
4. Check it: `autodoc --check-store` (below).
5. Start AutoDoc. The index is the backup's: files changed since are indexed
   again as the daemon finds them.

A backup from an older AutoDoc is brought up to date when the daemon opens it
(its schema scripts, [schema-scripts.md](schema-scripts.md)). A backup from a
newer AutoDoc is refused by an older one.

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

Each time the daemon opens the store it runs SQLite's `quick_check` first,
before any schema script writes. `quick_check` reads every page and record
but does not match each index to its table, which `integrity_check` does: it
costs one read of the file, so it stays fast on a large store. A store that
fails it is not served, since writing on a damaged store can spread the
damage. The daemon says so and names `--check-store` and this page. A busy,
locked or unreadable store is reported as the error it is, never as damage.

If the store is damaged, restore it from a backup as above. With no backup,
move the damaged store aside and start AutoDoc on a new one: add the
workspaces again, and their index (documents, chunks, links and vectors) is
built again from their files. What lived only in the store is lost: the
workspaces' settings and connections, the todo stores' settings, the crew's
sessions and mail, preferences, and the providers and rankers.
