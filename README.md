# DuruSQL

Fast, DataGrip-style database client for Linux, Windows and macOS. Go + Wails v2 + Svelte. MIT licensed.
**Beta**: things work, edges are rough; please open issues.

Supports MySQL/MariaDB, PostgreSQL and OpenSearch/Elasticsearch (read-only, SQL + REST console), SSH tunnels (agent / key / password),
multiple grouped connections with favorites, per-connection saved queries
(plain `.sql` files, git-friendly) and per-connection favorite tables.

## Run (one click)

```sh
./run.sh
```

That's it. On first run it installs everything it needs (Wails CLI, npm packages, Go modules,
and the GTK/WebKit dev packages via `sudo apt` — you'll be asked for your password once),
builds the app, and starts it. It also registers **DuruSQL** in the desktop app launcher,
so after the first run you can start it from the Activities/app grid like any other app.

Later runs only rebuild when a source file changed, so startup is instant.

```sh
./run.sh dev     # hot reload (wails dev)
./run.sh build   # build only → build/bin/durusql
./run.sh clean   # remove build output, node_modules and generated bindings
```

Requirements you must have already: Go ≥ 1.22 and Node/npm (`sudo apt install golang-go nodejs npm`).
Ubuntu 24.04+ ships WebKitGTK 4.1 only; the script detects this and builds with `-tags webkit2_41`.

## Import from DataGrip, PhpStorm and other JetBrains IDEs

The import button in the explorer opens a dialog. The main path is an **exported file**: a settings
export (File → Manage IDE Settings → Export Settings, a `.zip`), a project's `.idea/dataSources.xml`,
or the XML from *Copy Settings* saved to a file. SSH configs inside a `.zip` are used as well.
Passwords are never inside exported files: they are taken from this machine's keyring when the
data source was used here, otherwise fill them in after the import. Other sources:

- **DataGrip**: everything under `~/DataGripProjects` (same as the headless command below).
- **PhpStorm / IntelliJ / GoLand / WebStorm projects**: pick your workspace folder (scanned 6 levels
  deep for `.idea/dataSources.xml`, skipping node_modules/vendor) or a single `dataSources.xml`.
  Project data sources are grouped by project name.
- **Pasted XML**: right-click a data source in the IDE → *Copy Settings*, paste the XML.

SSH configs are merged from every JetBrains IDE installed (`~/.config/JetBrains/*/options/sshConfigs.xml`)
and saved passwords come from the system keyring, which all IDEs share. Re-importing updates
connections in place (id `dg-<uuid>`) and keeps your favorite, color and group.

Headless DataGrip import:

```sh
./build/bin/durusql -import-datagrip
```

It reads `~/DataGripProjects/*/.idea/dataSources{,.local}.xml` and the newest
`~/.config/JetBrains/DataGrip*/options/sshConfigs.xml`, and pulls saved DB passwords,
SSH passwords and key passphrases from the system keyring (Secret Service / GNOME Keyring).
Imported connections get the id `dg-<datagrip uuid>`, so re-importing updates them in place
and keeps your favorite/color tweaks. MySQL, MariaDB and PostgreSQL sources are supported;
others are skipped with a warning.

## Releases, channels and auto-update

```sh
echo 0.6.0-beta.1 > VERSION                            # a suffix (-beta.1) = pre-release → beta channel
echo https://github.com/makbulut/durusql > UPDATE_SOURCE   # compiled into the app as the default release source
./package.sh                                           # builds dist/ + SHA256SUMS + beta.json
./publish.sh                                           # gh release create v0.6.0-beta.1 --prerelease dist/*
```

The app checks the release source 5 s after start and once a day (open the dialog from the version
in the status bar). **stable** = latest GitHub release, **beta** = includes pre-releases. When a newer
version exists the status bar shows `⬆ x.y.z available`; the dialog shows the release notes, downloads
the package for this installation (verified against `SHA256SUMS`) and installs it: portable Linux and
Windows binaries are replaced in place and the app restarts, `.deb` installs via `pkexec apt-get`,
macOS opens the disk image. "Skip this version" silences one release. A self-hosted channel works
too: point the source at a URL serving the `stable.json` / `beta.json` that `package.sh` writes
(`DURUSQL_PUBLISH=user@host:/var/www/durusql ./publish.sh` uploads `dist/`).

## Packaging / distribution

```sh
./package.sh 0.6.0-beta.1      # → dist/
```

Produces `durusql_<ver>_amd64.deb` (Ubuntu 22.04+ / Debian 12+; `sudo apt install ./durusql_…deb`),
`durusql-<ver>-linux-amd64.tar.gz` (portable; run its `install.sh` for a per-user install without
root) and `durusql-<ver>-windows-amd64.zip` (cross-compiled; needs the WebView2 runtime that
Windows 10/11 already have). With `makensis` installed it also builds a Windows installer.
macOS cannot be cross-compiled. Build it once on any Mac (Intel or Apple Silicon, the result is
a universal app):

```sh
xcode-select --install                      # Xcode command line tools (once)
brew install go node                        # toolchain
git clone https://github.com/makbulut/durusql && cd durusql        # or copy the project folder
./package.sh 0.3.0                          # → dist/durusql-<ver>-macos.dmg and .zip
```

The script installs the Wails CLI and frontend packages itself, builds `durusql.app`, signs it
ad-hoc (required on Apple Silicon) and wraps it in a `.dmg` with an Applications shortcut.
Users drag it to Applications; on first start they right-click → Open because it is not notarized.
If macOS reports the app as damaged: `xattr -dr com.apple.quarantine /Applications/durusql.app`.
For dumps/restores install the client tools: `brew install mysql-client libpq` and put their
`bin` directories on the PATH (brew prints the export line). Connections live in
`~/Library/Application Support/durusql`.

Packages contain only the binary, a desktop entry, an icon and this README. **Connections,
passwords, saved queries and history are never included**: they live per user in
`~/.config/durusql` (Linux), `~/Library/Application Support/durusql` (macOS) or `%AppData%\durusql`
(Windows). Every user imports or creates their own connections. Dumps/restores need
`mariadb-client`/`mysql-client` or `postgresql-client` on the target machine.

## Data layout

```
~/.config/durusql/
  connections.yaml            all connections (passwords included — see TODO 1)
  <conn-id>/queries/*.sql     saved queries for that connection
  <conn-id>/favorites.json    favorite tables
  <conn-id>/history.jsonl     query history
```

## UI

DataGrip-style layout, frameless window with its own title bar (drag it to move, double-click to
maximise) and a status bar showing the connection › database › table breadcrumb and the memory
used by the app (DuruSQL plus its WebKit processes; hover for Go heap and goroutines).
Database explorer tree on the left, console tabs with SQL editor + results in the middle, Files
panel on the right, output / history panel at the bottom. All panes are drag-resizable (sizes are
remembered).

- Console tabs: every tab is its own SQL console bound to one connection, with its own result grid and pending edits. Ctrl+T new console, Ctrl+W close, Ctrl+PageUp/PageDown switch, double-click a tab to rename, right-click for close others / close all. Open tabs (SQL + binding) are restored on the next start.
- **Search everywhere** (magnifier in the explorer header, Ctrl+K or Ctrl+Shift+F): type a name and every connected server is searched through information_schema for tables, views, routines and columns, plus connections by name. Enter opens the hit (table data / routine source / connection), Shift+Enter only reveals and selects it in the explorer.
- Clicking a connection or database in the explorer binds the active console to it. The console header has a database selector: unqualified table names in the console resolve against it (MySQL `USE`, PostgreSQL `search_path`). Saved queries belong to a connection, not a database, so they open with no database selected; pick one to run. New consoles start on the database last clicked in the explorer
- Double-click a connection (or click its chevron) — connect and list its databases (schemas on PostgreSQL)
- Expand a database — tables, views, routines and (MySQL) events folders with counts; expand a table — columns (type, not null, extra), keys, foreign keys, indexes, triggers and partitions, like DataGrip
- Click table — opens it in a data-view tab (no editor): WHERE / ORDER BY fields (Enter applies), click a header to sort, hover a header for the funnel icon to add a column filter (=, ≠, contains, starts/ends with, comparisons, in list, is NULL / not NULL / empty; active filters show as chips and can be edited or removed), paging (500 rows per page by default), add/delete/submit/revert, CSV, "SQL" shows the generated query and "Console" opens it in a new console tab. Double-click — put `SELECT * …` in the active console
- Views: right-click → Open, DDL in console (SHOW CREATE VIEW / pg_get_viewdef). Routines: right-click → Source in console (SHOW CREATE PROCEDURE/FUNCTION / pg_get_functiondef), CALL in console
- Structure editing, DataGrip's Modify dialog: database → **New table…**, table → **Modify table…** / Add column… / Add index…; columns, keys, indexes and foreign keys in the explorer → double-click or right-click → Modify / Add / Drop. The dialog has the object tree on the left (columns, keys, foreign keys, indexes; + − ▲ ▼ toolbar), a property form on the right (table: name, comment, engine, collation; column: name, type, default, comment, not null, auto-increment/identity, primary key; key: primary/unique + columns; index: name, unique, columns; foreign key: columns, target table and columns, ON DELETE / ON UPDATE) and a live preview of the generated CREATE / ALTER SQL for MySQL or PostgreSQL. OK runs it, or open it in a console. **Rename table…**, **Drop table…** (type the name to confirm). Table DDL for PostgreSQL comes from `pg_dump --schema-only`
- Right-click a connection, database or table — context menu. Tables: open, SELECT in console, count rows, DDL in console (MySQL), favorites, copy name, **Export to CSV…** (streams all rows to a file), **Export with mysqldump/pg_dump…**, **Truncate table…** (confirmed). Databases: new console, copy name, dump.
- Dump dialog modelled on DataGrip's: executable path, output path with `{timestamp}` `{data_source}` `{database}` `{table}` patterns (default `~/durusql-dumps/`), database/tables/extra arguments, the usual option checkboxes, and a live preview of the command line. Uses `mariadb-dump`/`mysqldump`/`pg_dump` from PATH (install mariadb-client or postgresql-client); SSH connections get a local port forwarded through the tunnel. Options are remembered per driver.
- Restore dialog ("Restore with mysql…" / "Restore with psql…" on a database or connection): client executable, dump file (`.sql` or `.sql.gz`, decompressed on the fly), target database (with the known databases as suggestions), extra arguments, and options: create the database if missing, disable foreign key / unique checks, continue after errors (MySQL); ON_ERROR_STOP, single transaction (PostgreSQL). Shows the command line, asks for confirmation, and refreshes the explorer afterwards.
- Files panel on the right (like DataGrip's Scratches and Consoles): every connection's saved queries as `.sql` files. Click to open in a console bound to that connection, right-click for Run / Rename / Delete, **+** for a new file. Ctrl+S saves the console back to its file (Ctrl+Shift+S saves as…); a ● on the tab marks unsaved changes. The panel is resizable and can be hidden (reopen from the button next to the tab list)
- Autocomplete (Ctrl+Space, or just type): keywords, databases, tables and views of the bound connection, and columns with their types after `table.` or an alias. Column names are fetched per database with one query when it is expanded or bound
- Ctrl+Enter — run whole editor, or just the selection. Several statements (split on `;`, DELIMITER understood) run one after another with one result tab per statement; execution stops at the first error and earlier results stay. The ■ button cancels a running statement.
- Tx: Auto / Manual in the header: Manual keeps a transaction open on the connection (grid edits join it) until you press ✓ Commit or ✕ Rollback; the pending statement count is shown
- Double-click an Output row — load that statement back into the editor

### Editing data

Results of a single-table `SELECT` (table previews included) are editable, like DataGrip's
data editor. Changes are collected locally and written in one transaction on **Submit**.

- Double-click a cell, or press Enter / F2 / start typing — edit; Enter commits and moves down, Tab moves right, Esc cancels
- Arrow keys move; Shift+arrows / Shift+click select a block; click a row number to select rows; Ctrl+A all
- Ctrl+C copies the selection as TSV, Ctrl+V pastes TSV starting at the focused cell (a single value pasted onto a block fills it)
- Delete / Backspace — set the selected cells to NULL, or delete the rows when whole rows are selected; Ctrl+Y deletes rows
- Alt+Insert or **+** — add a row; right-click for the full menu (Set NULL, Undelete, Revert selection, …)
- Shift+Enter or right-click → View / edit value: cell viewer (pretty-prints JSON, edit and Set value); Row details… shows the row transposed
- Drag a header to reorder columns, drag its right edge to resize (double-click resets); right-click → Copy as INSERT / Copy as Markdown table; Export ▾ writes the full result as CSV, JSON or Excel
- **Submit** (Ctrl+Enter inside the grid) — writes edits (yellow), deletes (red, struck through) and inserts (green); **Revert** drops them all

Rows are matched by primary key (🔑 columns). Without a key, all original values are used and the
grid shows a **no key** badge. Every UPDATE/DELETE must hit exactly one row, otherwise the whole
batch is rolled back. Joins, aggregates and subqueries stay read-only.

## Done (2026-09-17)

- Import from DataGrip / PhpStorm / other JetBrains IDEs (files, folder scan, exported .zip, pasted XML), passwords from the keyring
- Explorer: connection → database → tables / views / routines / events, table structure (columns, keys, FKs, indexes, triggers, partitions), PostgreSQL sequences, object types, extensions, languages
- Console tabs with per-tab database selector, autocomplete, Ctrl+Enter, multi-statement scripts with result tabs, cancel, Tx Auto/Manual with commit/rollback, saved queries (Files panel, Ctrl+S, copy/move between connections), history panel
- Table data view: WHERE / ORDER BY, column filters, sorting, paging, editable grid with submit/revert, column resize/reorder, value viewer, row details, copy as INSERT/Markdown, CSV/JSON/Excel export
- Structure: create/alter/rename/drop table dialogs, DDL of tables/views, routine source
- mysqldump / pg_dump export and mysql / psql restore dialogs (SSH tunnelled), truncate, DDL
- Frameless DataGrip-like UI, status bar with memory use, packaging for Linux (.deb, tar.gz) and Windows (zip)

## TODO / next

1. **Passwords into the OS keyring** instead of `connections.yaml` (deferred by choice for now)
2. **Known-hosts prompt** on first SSH connection; prompt for missing SSH secrets
3. **Light theme** and follow the system theme
4. **Windows / macOS verification** on real machines
5. Structure editing, next steps: check constraints, partitions and virtual columns in the Modify dialog; view / routine editors
