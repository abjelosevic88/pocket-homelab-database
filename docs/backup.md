# Backup

- `DATA_DIR/pocket-database.db` — connections, history, saved queries, settings. Back it up with `pocket-database backup` or Settings → Backup & data (a consistent `VACUUM INTO` copy); never copy the live file.
- `DATA_DIR/secret.key` — seals the connection passwords. Without it the passwords have to be typed again.
- `DATA_DIR/snapshots/` — copies of SQLite files taken before changes; `DATA_DIR/files/` — uploaded SQLite files. Both can be large; skip them if you back up the databases themselves.
- Restore: stop the app, put the files back, start it.
