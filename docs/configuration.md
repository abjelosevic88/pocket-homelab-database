# Configuration

Environment variables; every one has a default. Personal settings (tables, the editor, safety, alerts, AI) live in the UI per person — the `NOTIFY_*`/`OPENAI_*` values below are copied once into the first admin's settings.

| Variable | Default | What |
|---|---|---|
| `PORT`, `HOST` | `8080`, `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL (links in pushes, the OIDC callback) |
| `DATA_DIR` | `./data` | App database, `secret.key`, snapshots, uploaded files, Core token |
| `DB_PATH` | `$DATA_DIR/pocket-database.db` | The app's own SQLite file |
| `MAX_BODY_BYTES` | `16777216` | Largest JSON request (an import) |
| `UPLOAD_MAX_BYTES` | `1073741824` | Largest SQLite file that can be uploaded |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Container engine for discovery and pausing apps (`tcp://…` works; `off` turns both off) |
| `HOST_ROOT` | – | Where the server's file system appears inside the container (`/host` when you mount `/:/host`); empty = mounted at the same paths |
| `SCAN_PATHS` | – | More folders (server paths, comma separated) to look for SQLite files in, e.g. a natively run app's data |
| `SELF_CONTAINER` | `pocket-database` | This app's own container, left out of discovery |
| `TZ` | – | Time zone |
| `AUTH_MODE` | `local` | `local` (accounts), `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME` | – | Seed the first admin while the database is empty |
| `SESSION_TTL`, `SECURE_COOKIES` | `720h`, `false` | Set `SECURE_COOKIES=true` behind HTTPS |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME`, `OIDC_AUTO_CREATE` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL`, `NOTIFY_FORMAT` | – | Apprise (`…/notify/key`), ntfy topic URL or JSON webhook; format `apprise`/`ntfy`/`json` |
| `OPENAI_API_KEY`, `OPENAI_MODEL`, `OPENAI_BASE_URL` | – | Writing and explaining queries without Pocket AI |
| `CORE_URL`, `CORE_REGISTRATION_KEY`, `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY`, `METRICS_ENABLED`, `LOG_LEVEL`, `LOG_FORMAT` | `true`, `true`, `info`, `text` | |
| `DEMO_DATA` | `false` | A shop database (a real SQLite file), a notes file, some servers on paper, history and growth in the owner account |

Commands: `pocket-database backup [-o FILE]`, `pocket-database reset-password [-email] [-password]`, `pocket-database version`.

## Reaching your databases

Run it with **host networking**: database containers usually sit on their own Docker networks, which the host can reach and a bridged container cannot. With the **Docker socket** mounted (and the docker group added), *On this server* lists every PostgreSQL, MySQL / MariaDB and Redis container with the user, password and database from its environment, and every SQLite file in the folders containers mount — as long as those folders are visible inside at the same path (or under `HOST_ROOT`). Connections to containers look up the container's address on every connect, so a recreated container keeps working.

The socket is powerful (it can stop and start any container): only admins can discover, open files on the server or connect to containers, and the app only ever stops and starts the containers that use a SQLite file you are changing.
