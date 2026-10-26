# API

Everything the app does is a JSON API under `/api/v1`. Authenticate with a session cookie, a personal token (`Authorization: Bearer pdb_…`, Settings → API & shortcuts) or, for Pocket apps, a Pocket Core link token. Every connection, query and change belongs to the person whose token it is; another person's ids answer 404.

| Endpoint | What |
|---|---|
| `GET /overview` | Counts (up, down, paused), total size, connections, growth over 30 days, biggest, latest changes |
| `GET/POST /connections`, `GET/PUT/DELETE /connections/{id}` | Connections (passwords are write-only and sealed) |
| `POST /connections/test` | Try a connection from the form |
| `POST /connections/upload` | Open an uploaded SQLite file (multipart `file`, `name`) |
| `POST /connections/{id}/check`, `GET /connections/{id}/sizes?days=90` | Check now; size history |
| `GET /connections/{id}/server`, `/databases`, `/schemas?db=` | The server, its databases, the schemas and tables of one |
| `GET /connections/{id}/table?db=&schema=&name=` | Columns, keys, indexes, foreign keys both ways, the CREATE statement |
| `GET /connections/{id}/rows?db=&schema=&name=&limit=&offset=&sort=&desc=&search=&filters=` | A page of rows; `filters` is JSON `[{"column","op","value"}]` with op `eq ne lt le gt ge contains starts ends null notnull` |
| `POST /connections/{id}/rows` | `{"db","schema","name","changes":[{"op":"update","key":{…},"values":{…}}, {"op":"insert","values":{…}}, {"op":"delete","key":{…}}]}` — one transaction |
| `POST /connections/{id}/query` | `{"db","sql","confirm"}` — runs a script as one transaction; a change that deserves a second look answers `{"needs_confirm":true,"reasons":[…]}` until sent again with `confirm: true` |
| `GET /connections/{id}/export?format=csv\|json&name=` or `&sql=` | A table or a query, streamed |
| `GET /connections/{id}/keys?db=&pattern=&cursor=`, `GET/PUT/DELETE /connections/{id}/key`, `POST /connections/{id}/key/meta`, `POST /connections/{id}/command` | Redis keys, values, expiry and rename, the console |
| `GET /connections/{id}/safety`, `POST /connections/{id}/pause` (`start`/`finish`/`extend`), `POST /connections/{id}/snapshots`, `POST /snapshots/{id}/restore`, `GET /snapshots/{id}/download` | Changing live SQLite files safely |
| `GET /discover`, `POST /discover/add` | Databases on this server (admins) |
| `GET/DELETE /history`, `GET/POST /saved`, `PUT/DELETE /saved/{id}`, `GET /activity` | History, saved queries, the change log |
| `POST /ai/sql` | `{"connection_id","db","mode":"write\|explain\|fix","prompt","sql","error"}` — never runs anything |

Changes need “Allow changes” on the connection (`409 {"code":"writes_off"}` otherwise). A SQLite file a running container uses answers `409 {"code":"needs_pause"}` until it is paused.

## Pocket Core

Offers `databases:read` (overview, connections without addresses or paths, structure, sizes) and `databases:query` (rows, keys and read-only queries). A linked app can never change anything. Uses Pocket AI. Events: `database.down`, `database.up`, `database.changed`, `database.restored`.
