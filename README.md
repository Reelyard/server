# Harbor

Harbor tracks the movies and TV series you want, finds torrents for them through
Stremio addons ("suppliers"), downloads them with a built-in torrent engine, and puts
them in a media library with Jellyfin/Plex naming and subtitles. It will also serve the
library over DLNA. One binary for Linux, macOS and Windows.

> Status: **M3 — library**. Suppliers, search, the built-in torrent engine, and importing into a
> Jellyfin/Plex-style library with subtitles work. Automatic tracking, seeding rules and DLNA
> arrive in later milestones; see [PLAN.md](PLAN.md).

Only download content you have the rights to.

## Run it

```sh
make web      # build the web UI (needs Node 20+)
make build    # build bin/harbor (needs Go 1.25+)
bin/harbor serve
```

Open <http://localhost:8787>. The first visit asks you to create the admin account.

Without `make` (e.g. on Windows):

```sh
cd web && npm ci && npm run build && cd ..
go build -o harbor.exe ./cmd/harbor
harbor.exe serve
```

## Install as a service

```sh
sudo harbor install          # system service: systemd (Linux), launchd (macOS)
sudo harbor start
harbor install --user        # or a per-user service, no root needed
harbor start --user
```

On Windows, run `harbor.exe install` and `harbor.exe start` from an administrator
terminal; Harbor runs as a Windows service.

`install` writes a config file into the data folder so the service and the CLI use
the same locations. Other commands: `stop`, `restart`, `status`, `uninstall`,
`reset-password`, `version`.

## Folders

| | Linux | macOS | Windows |
|---|---|---|---|
| Data (user) | `~/.local/share/harbor` | `~/Library/Application Support/Harbor` | `%ProgramData%\Harbor` |
| Data (system service) | `/var/lib/harbor` | `/Library/Application Support/Harbor` | `%ProgramData%\Harbor` |

Inside the data folder: `harbor.db`, `logs/`, `downloads/`, `library/`. The download and
library folders can live anywhere (`download_dir`, `library_dir`).

## Configuration

`config.yaml` in the data folder (or `--config <file>`):

```yaml
listen: ":8787"
data_dir: /srv/harbor
download_dir: /srv/harbor/downloads
library_dir: /srv/media
torrent_port: 42069   # peers connect here (TCP+UDP); forward it in your router
log:
  level: info     # debug | info | warn | error
  format: text    # text | json
  file: true      # rotating file in <data>/logs
```

Environment variables override the file: `HARBOR_LISTEN`, `HARBOR_DATA_DIR`,
`HARBOR_DOWNLOAD_DIR`, `HARBOR_LIBRARY_DIR`, `HARBOR_TORRENT_PORT`, `HARBOR_LOG_LEVEL`,
`HARBOR_LOG_FORMAT`, `HARBOR_LOG_FILE`.

Downloads land in `<download_dir>/<info hash>/…`. When one finishes, Harbor links it into the
library (a hardlink when both folders are on the same disk, so no extra space is used; a copy
otherwise):

```
Movies/Night of the Living Dead (1968)/Night of the Living Dead (1968) - 720p.mp4
Movies/Night of the Living Dead (1968)/Night of the Living Dead (1968) - 720p.en.srt
TV/Breaking Bad (2008)/Season 01/Breaking Bad (2008) - S01E02 - Cat's in the Bag - 1080p.mkv
```

Subtitles come from your subtitle suppliers (e.g. OpenSubtitles) for the languages chosen in
**Settings → Library**. If `ffprobe` (from FFmpeg) is installed, Harbor uses it to read the real
resolution and to skip languages already built into the video. Speed limits and how many downloads run at
once are set in **Settings → Downloads**.

## Forgot the password?

```sh
harbor reset-password                    # prompts for a new password
echo 'new password' | harbor reset-password --password-stdin
```

## API

Everything the UI does goes through `/api/v1`. Scripts can use an API key from
**Settings → API keys**, sent as the `X-Api-Key` header:

```sh
curl -H "X-Api-Key: hbr_..." http://localhost:8787/api/v1/system
```

## Development

```sh
go run ./cmd/harbor serve      # API on :8787
cd web && npm run dev          # UI on :5173 with hot reload, /api proxied to :8787
make test vet
make cross                     # all six OS/arch binaries into dist/
```

Layout: `cmd/harbor` (CLI), `internal/config`, `internal/logging`, `internal/store`
(SQLite + migrations), `internal/auth`, `internal/api` (HTTP + embedded UI),
`internal/app` (lifecycle), `web/` (React + Vite + Tailwind, embedded with `go:embed`).

## License

MIT — see [LICENSE](LICENSE).
