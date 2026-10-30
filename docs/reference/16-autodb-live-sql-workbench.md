# Chapter 16: AutoDB Live SQL Workbench

Technical notes, incident post-mortems, and architecture decision records rarely live in isolation from production and staging databases. Engineers constantly switch between writing prose in an editor and running diagnostic queries in a separate SQL client—copying queries out, losing track of which connection ran them, and manually reformatting tabular output back into Markdown.

By pairing AutoDoc's plugin host (Chapter 15) with the **AutoDB plugin** (`autodoc-autodb`, [plugin tables](../decisions/plugins.md#plugin-tables-in-the-results-panel)), AutoDoc becomes a full-featured **live SQL workbench**. You write SQL either in dedicated `.sql` buffers or inside fenced code blocks within your Markdown notes, execute the statement under your cursor with a single chord, browse 100,000-row virtualized cursors in the docked Results panel, and insert formatted Markdown tables or export atomic CSV/JSON snapshots without leaving your workspace.

---

## 16.1 Dialect-Aware SQL Authoring and Statement Splitting

AutoDoc natively highlights and parses SQL across four dialect families, whether you open a standalone `.sql` file (`kind = "registered"`), spawn an unsaved query draft via `buffer.new`, or write inside a fenced code block in a Markdown note:

- **PostgreSQL** (`*.pgsql`, ```` ```pgsql ````, ```` ```postgres ````)
- **MySQL / MariaDB** (`*.mysql.sql`, ```` ```mysql ````, ```` ```mariadb ````)
- **SQLite** (`*.sqlite.sql`, ```` ```sqlite ````)
- **Standard / Session-Bound SQL** (`*.sql`, ```` ```sql ````)

### Statement Splitting at `cursor_byte` (`golib/parse/sql`)

When you trigger **Run statement or selection** (`SPC p a r`: this chapter writes `a` for the letter you give AutoDB yourself, on **SPC p** with `*` and then a letter; AutoDoc assigns plugins no letter), AutoDoc sends `plugin.command {id: "run", document}` (Protocol 4/5) containing the exact buffer snapshot, `cursor_byte`, and `selection_bytes`.

If a visual selection is active (`selection_bytes` non-empty), `autodoc-autodb` executes that exact slice. Otherwise, `autodoc-autodb` feeds the buffer (or the fenced SQL block surrounding `cursor_byte`) into `golib/parse/sql` using the active connection's dialect. Because `golib/parse/sql` lexes strings, comments (`--` and `/* ... */`), PostgreSQL `$$tag$$` dollar-quoted bodies, quoted identifiers (`"..."`, `` `...` ``, `[...]`), and `BEGIN ... END` blocks, semicolons inside string literals or procedural bodies never prematurely split your query.

---

## 16.2 Connecting, Unlocking Sessions, and Browsing Schemas (`SPC p a c`)

`autodoc-autodb` runs as a **non-modal docked card** (`[dialog] modal = false`, `[feed] document = true`, `[table] title = "Results"`, and `calls` naming `buffer.new` among the host commands it uses), at the plugin protocol its manifest declares.

1. **Opening and Unlocking (`SPC p a c`)**: Pressing `SPC p a c` (or selecting **Plugins › AutoDB › Connect**) opens and focuses the AutoDB card at the right edge of your screen. If the AutoDB daemon's keystore is sealed, the card prompts for your passphrase and unlocks your saved connection profiles.
2. **Schema Inspection and Draft Generation**: Once connected to a PostgreSQL, MySQL/MariaDB, or SQLite target, the card displays your active connection, transaction/session status, and schema browser. Pressing `Enter` on any table or view invokes `host.call` with `buffer.new` (`calls = ["buffer.new"]`), opening a fresh unsaved tab named `<schema>.<table>.sql` pre-populated with:

```sql
SELECT *
FROM public.tracks
LIMIT 100;
```

3. **Persistent Background Session**: Pressing `Esc` while focused in the AutoDB card hides its side panel (`plugin.hide`) and returns keyboard focus to your editor, while keeping the `autodoc-autodb` process and its authenticated database session alive in the background.

---

## 16.3 Executing Queries and Demand-Paged Session Cursors

AutoDoc and AutoDB never buffer an entire multi-million-row `SELECT` into client RAM. Instead, query execution streams through a demand-paged session cursor:

![AutoDB Paged Cursor Flow](autodb-paged-cursor-flow.mmd)

- **Statement at Cursor (`SPC p a r`) vs. Full Script (`SPC p a s`)**: Pressing `SPC p a r` runs the selected range or the single statement under the cursor; pressing `SPC p a s` runs the entire SQL script or fenced block. To abort a long-running query, press `SPC p a x`.
- **Session Cursor Paging (`exec.session_query` / `exec.session_fetch`)**: `autodoc-autodb` submits the query to the AutoDB daemon via `exec.session_query`, which opens a server-side cursor and returns the column headers, the first page of rows, and their Protocol 5 cell `kinds` (`t` text, `z` NULL, `n` number, `b` boolean, `j` JSON), sized carefully within the 1 MiB msgpack frame limit (`plugin.EncodedSize`).
- **On-Demand Fetching**: `autodoc-autodb` sends `host.table {id, title, fields, rows, kinds, more}` to AutoDoc, which opens the bottom **Results panel** without stealing focus from your editor. As you scroll down near the end of the loaded rows, AutoDoc emits `plugin.table.fetch {id, n}`, which `autodoc-autodb` forwards to `exec.session_fetch` and answers with `host.rows {id, rows, kinds, more}` up to the 100,000-row view cap. When you run a new query or close the Results panel, AutoDoc sends `plugin.table.close {id}` so AutoDB immediately releases the database cursor.

![Resident Client Memory Across Result Set Sizes](sql-cursor-paging-memory.chart.json)

---

## 16.4 Navigating and Sorting the Results Panel (`TableView`)

When focused on the bottom Results panel, you navigate cells and rows using modal Vim motions:

- **Cell and Row Motions**: `h`, `j`, `k`, `l` move across cells and rows (with numeric counts such as `25j`); `w` and `b` jump forward and backward by column; `gg` and `G` jump to the first and last loaded row.
- **Visual Selection**: `v` selects a range of cells in reading order, `V` selects whole rows, and `Ctrl+V` selects a rectangular column/row block. You can also drag with the mouse across cells, row numbers, or column headers.
- **Protocol-5 Typed Sorting (`s` and the `ORDER BY` Bar)**: Pressing `s` sorts the loaded rows by the column under the cursor. Above the table sits an interactive **clause bar** with column completion where you can type SQL-style sort expressions such as `created_at DESC, id ASC` (or reference duplicate column names by 1-based position). Because `autodoc-autodb` supplies Protocol 5 `kinds`, AutoDoc sorts database `NULL` (`z`) last in both ascending and descending directions, compares numeric columns (`n`) by exact decimal value rather than ASCII byte order, and orders `false` before `true` (`b`).

---

## 16.5 Copying, Atomic `.part` Exports, and Markdown Insertion

Every copy and export action in the Results panel operates deterministically on the **rows currently loaded in the view** (never triggering hidden background fetches):

1. **Clipboard Copy (`y` / `results.copy`)**: Pressing `y` (or running `:results.copy`) copies the active cell or selection to the clipboard as tab-separated values (`TSV`). Companion commands `results.copy_csv`, `results.copy_json`, and `results.copy_markdown` format the selection with column headers as CSV, a typed JSON array, or a GitHub-Flavored Markdown table (capped at 16 MiB).
2. **Atomic Crash-Safe File Export (`results.export` / `results.csv`)**: Running `results.export` (or `results.csv`) opens a dialog stating the exact number of loaded rows to be written. AutoDoc streams the output to a sibling temporary file `<filename>.part` and atomically renames it over `<filename>` only after the final byte is flushed. If an I/O error occurs mid-write, `<filename>.part` is unlinked and any existing target file remains untouched. Under Protocol 5, JSON exports write SQL `NULL` as literal `null`, numbers as exact unquoted numeric literals, booleans as `true`/`false`, and `j` cells as nested JSON structures.
3. **Direct Markdown Note Insertion (`results.markdown`)**: Running `results.markdown` (or choosing **Insert as Markdown** from the Results right-click menu) formats the loaded rows into an aligned Markdown table and inserts it directly at your editor cursor as a single undoable step.

---

## 16.6 Hands-On Tutorial: Authoring a Live Incident Post-Mortem with AutoDB

Suppose you are writing an incident analysis in `synthesis/2026-10-17-checkout-latency-spike.md` and want to document slow database queries, embed the exact evidence table as a companion `.csv` asset, and keep the reproducible SQL inside the note.

### Step 1: Write the Diagnostic Query Inside Your Markdown Note

Open `synthesis/2026-10-17-checkout-latency-spike.md` in AutoDoc and add a fenced `pgsql` block containing multiple diagnostic statements:

````markdown
## Database Lock & Query Evidence

```pgsql
-- Statement 1: Inspect top 10 slowest queries by mean execution time
SELECT queryid,
       calls,
       ROUND(mean_exec_time::numeric, 2) AS mean_ms,
       ROUND(total_exec_time::numeric, 1) AS total_ms,
       LEFT(query, 80) AS sql_preview
FROM pg_stat_statements
WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER BY mean_exec_time DESC
LIMIT 10;

-- Statement 2: Check active ungranted row/table locks
SELECT pid, mode, relation::regclass AS locked_table, wait_event
FROM pg_locks JOIN pg_stat_activity USING (pid)
WHERE NOT granted;
```
````

### Step 2: Connect and Execute Statement 1 at the Cursor

1. Press **`SPC p a c`** to focus the docked AutoDB card, select your staging/read-replica PostgreSQL profile, and press `Esc` to return focus to your Markdown buffer.
2. Place your cursor anywhere inside **Statement 1** and press **`SPC p a r`**.
3. `autodoc-autodb` receives `plugin.command {id: "run", document}`, extracts the `pgsql` fenced block around `cursor_byte`, splits Statement 1 cleanly via `golib/parse/sql`, executes `exec.session_query`, and opens the bottom **Results panel** displaying the 10 rows with Protocol 5 kinds `"tnnnt"`.

### Step 3: Sort and Export to Your Note's Companion Directory

1. Focus the Results panel and press `s` on the `total_ms` column to re-sort the loaded rows by cumulative impact (using exact decimal numeric comparison rather than string sorting).
2. Run **`:results.csv`** and save the file to `synthesis/2026-10-17-checkout-latency-spike/top-slow-queries.csv`. AutoDoc writes `top-slow-queries.csv.part`, flushes all rows, and atomically renames it into your note's companion directory.
3. Below your `pgsql` code block in `synthesis/2026-10-17-checkout-latency-spike.md`, add a standalone embed line:

```markdown
![Top 10 Slowest PostgreSQL Queries During Incident Window](top-slow-queries.csv)
```

4. Alternatively, for a smaller 3-row summary from **Statement 2**, move your cursor into Statement 2, press **`SPC p a r`**, and run **`:results.markdown`** to insert the formatted Markdown pipe table directly into your prose as a single undo step (`u` reverts the insertion cleanly).

---

## 16.7 Transaction Safety and Read-Only Guardrails

When querying production or staging databases from inside documentation notes, `autodoc-autodb` and the AutoDB daemon enforce several safety boundaries:

- **Explicit Read-Only Profiles**: Connection profiles configured with `read_only = true` in AutoDB run inside `SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY`, ensuring accidental `UPDATE`, `DELETE`, or `DROP` statements inside a note are rejected by the database engine.
- **Uncommitted Transaction Indicator**: If you run `BEGIN;` inside a session via `SPC p a r`, the AutoDB card header highlights the active transaction state (`TX: IN TRANSACTION`) until you run `COMMIT;` or `ROLLBACK;` (or cancel via `SPC p a x`).
- **Automatic Cursor Cleanup on Buffer or Query Switch**: Whenever you run a new statement, close the Results panel, or quit AutoDoc, `plugin.table.close {id}` closes the server-side cursor immediately so no idle portal holds locks or server memory.

![AutoDB Commands, Results Actions, and SQL Dialects Reference](autodb-commands-and-dialects.csv)

---

[← Previous: Chapter 15: Plugin Architecture & Registry](./15-plugin-architecture-and-registry.md) | [Index](./README.md) | [Next: Chapter 17: Notebooks in Markdown — The Jupyter Plugin →](./17-jupyter-notebooks-in-markdown.md)

