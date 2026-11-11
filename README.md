# Harbor

Harbor tracks the movies and TV series you want, finds torrents for them through
Stremio addons ("suppliers"), downloads them with a built-in torrent engine, and puts
them in a media library with Jellyfin/Plex naming and subtitles. It also serves the
library to TVs over DLNA. One binary for Linux, macOS and Windows.

> Status: **M6 — DLNA and media servers**. Suppliers, search, the built-in torrent engine,
> importing into a Jellyfin/Plex-style library with subtitles, quality profiles, automatic
> tracking, sharing limits, stall detection, disk-space checks, VPN binding, the DLNA server
> and Jellyfin/Emby/Plex refresh work. Packaging arrives next; see [PLAN.md](PLAN.md).

## Watching on a TV (DLNA)

Harbor has its own DLNA server, on by default. TVs, game consoles and players like VLC on
your network list it as **Harbor** (rename it in **Settings → DLNA server**) and play straight
from the library, with seeking. Subtitles saved next to a video are offered with it, in your
subtitle-language order (Samsung TVs get them too). Thumbnails need `ffmpeg`, durations
`ffprobe`. Turn on converted versions there for TVs that can't play a file (needs `ffmpeg`).

- It uses TCP port **8200** and SSDP (UDP 1900, multicast); allow both in your firewall.
- Only devices on private networks (192.168.x, 10.x, 172.16–31.x, link-local) can connect.
- It isn't announced on your VPN interface or, unless you choose them, on Docker/VM bridges.

## Jellyfin, Emby and Plex

Point your media server's Movies and Shows libraries at `library/Movies` and `library/TV`.
In **Settings → Jellyfin, Emby and Plex**, add the server's address and an API key (Jellyfin,
Emby) or `X-Plex-Token` (Plex); **Test connection** checks both. Harbor then asks it to rescan
about 30 seconds after new files arrive (several imports close together share one rescan).
Keys are stored in Harbor's database and never sent back to the browser.

## Sharing, stalls and disk space

Finished downloads keep sharing until the title's profile ratio or time limit (for downloads
you started by hand: **Settings → Downloads**), whichever comes first. Then sharing stops,
and if the file is in your library its download copy is deleted; the library file stays
(it's a hardlink or a copy). Downloads that make no progress for 30 minutes (adjustable) fail;
tracked titles then try another release. Downloads that wouldn't fit on disk, keeping 2 GB free
(adjustable), are refused.

## Using a VPN

Set `bind_interface` to your VPN's network interface (`wg0`, `tun0`, …) and restart Harbor.
Every torrent connection — peers, DHT, trackers — is then bound to that interface's address,
so torrent traffic can't leave by any other route. If the interface goes down, torrents pause
and the Downloads page says so; they resume when it's back. If the VPN comes back with a
different address, Harbor (when running as a service) restarts itself to use it.

## Tracking titles

Open a movie or series and press **Track**. Harbor then looks for it by itself (every minute
for new work, backing off from 15 minutes to a day when nothing fits), picks the best release
for the title's **quality profile**, downloads it, and imports it. Series follow new episodes
as they air; choose all episodes, only new ones, or none.

A profile sets the screen sizes allowed (best first), file-size limits for movies and
episodes, preferred or required audio languages, subtitle languages, words to avoid or
prefer, and the minimum number of seeders. Three profiles are created on first start
(*HD 1080p*, *4K*, *Small*); edit them under **Quality profiles**. On a tracked title's page
every stream shows whether it fits the profile, and why not.

If a download is removed, fails, or turns out to have the wrong audio language (checked with
ffprobe when the profile requires one), that release is put on a blocklist for that title
and Harbor picks the next best.

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
bind_interface: wg0   # optional: torrents only use this interface (e.g. your VPN)
log:
  level: info     # debug | info | warn | error
  format: text    # text | json
  file: true      # rotating file in <data>/logs
```

Environment variables override the file: `HARBOR_LISTEN`, `HARBOR_DATA_DIR`,
`HARBOR_DOWNLOAD_DIR`, `HARBOR_LIBRARY_DIR`, `HARBOR_TORRENT_PORT`, `HARBOR_BIND_INTERFACE`, `HARBOR_LOG_LEVEL`,
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
