# Changelog

## Unreleased

- OpenSearch / Elasticsearch connections (driver "OpenSearch / Elasticsearch", port 9200, optional HTTPS with a skip-certificate-check switch, SSH tunnel). Indices are listed as tables under the cluster name, mappings as columns (nested fields flattened to dotted paths), aliases as views. The index view pages and filters through the SQL plugin; the console runs SQL statements and Dev Tools style REST requests (`GET /index/_search` followed by a JSON body), one result tab per request; search hits, `_cat` listings and plain objects become grid rows. Read-only: no grid edits, Modify dialog, dumps or transactions. "Mapping in console" shows settings + mappings as a replayable `PUT`; "Delete all documents…" runs `_delete_by_query`.

## 0.6.0-beta.1 (2026-09-18)

First public beta of DuruSQL (formerly dbtool).

- DataGrip-style explorer: connections → databases → tables / views / routines / events, table structure (columns, keys, foreign keys, indexes, triggers, partitions), PostgreSQL sequences, object types, extensions
- Console tabs with database selector, autocomplete, multi-statement scripts, cancel, manual transactions, saved queries (Files panel), history
- Table data view: filters, sorting, paging, editable grid (submit / revert), column resize and reorder, value viewer, row details, CSV / JSON / Excel export
- Modify dialog for tables (columns, keys, foreign keys, indexes) generating DataGrip-style SQL for MySQL / MariaDB and PostgreSQL
- mysqldump / pg_dump export and mysql / psql restore dialogs, SSH tunnelled
- Import connections from DataGrip, PhpStorm and other JetBrains IDEs (exported files, folder scan, pasted XML)
- Search everywhere across connected servers
- Updates from GitHub Releases (stable / beta channels) with self-update
- Packages for Linux (.deb, tar.gz) and Windows (zip); macOS build script
