# Reverse proxy

Any proxy works; see `deploy/reverse-proxy/Caddyfile`. Raise the body size limit for uploading SQLite files (`UPLOAD_MAX_BYTES`, 1 GB by default), keep `flush_interval -1` (or proxy buffering off) for streamed exports, and set `SECURE_COOKIES=true` and the real `BASE_URL`.
