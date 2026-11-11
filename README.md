# Reelyard

Reelyard tracks the movies and TV series you want, finds torrents for them through
Stremio addons ("suppliers"), downloads them with a built-in torrent engine, and puts
them in a media library with Jellyfin/Plex naming and subtitles. It also serves the
library to TVs over DLNA. One binary for Linux, macOS and Windows.

> Status: **M7 — packaging**. Suppliers, search, the built-in torrent engine,
> importing into a Jellyfin/Plex-style library with subtitles, quality profiles, automatic
> tracking, sharing limits, stall detection, disk-space checks, VPN binding, the DLNA server,
> Jellyfin/Emby/Plex refresh, packages for Linux, macOS, Windows and Docker, and a first-run
> setup work; see [PLAN.md](PLAN.md).

## Install

Downloads are on the [releases page](https://github.com/reelyard/server/releases). After
installing, open <http://localhost:8787>: the first visit creates your account and walks
you through folders and suppliers.

**Debian, Ubuntu, Raspberry Pi OS** (`.deb`) or **Fedora, RHEL, openSUSE** (`.rpm`):

```sh
sudo apt install ./reelyard_*_amd64.deb      # or: sudo dnf install ./reelyard-*.x86_64.rpm
```

The package creates a `reelyard` user, stores everything in `/var/lib/reelyard`, and starts
the `reelyard` systemd service (`systemctl status reelyard`). Install `ffmpeg` too for
language checks, thumbnails and TV transcoding. To keep media elsewhere, change the
folders in the setup wizard, then give the service access to them with
`sudo systemctl edit reelyard` → `[Service]` `ReadWritePaths=/srv/media`. Removing the
package keeps your data.

**macOS** (Homebrew):

```sh
brew install --cask reelyard/tap/reelyard
reelyard install --user && reelyard start --user    # start now and at every login
```

Or download the `darwin` archive and run `xattr -d com.apple.quarantine reelyard` once:
the binaries aren't notarized.

**Windows:** download the `windows` zip and follow `INSTALL.txt` inside: double-click
`reelyard.exe` to try it, or install it as a Windows service from an administrator
terminal (`reelyard.exe install`, `reelyard.exe start`), plus the firewall rules listed there.

**Docker** (NAS and home servers):

```sh
docker run -d --name reelyard --network host --restart unless-stopped \
  -v reelyard-config:/config -v /srv/media:/data \
  ghcr.io/reelyard/server:latest
```

`/data` holds `downloads/` and `library/`; keeping both on that one volume lets imports
use hardlinks instead of copies. `--network host` is needed for DLNA discovery (TVs find
servers by multicast); without DLNA you can publish ports instead:
`-p 8787:8787 -p 42069:42069 -p 42069:42069/udp`. The image runs as user 1000 and
includes ffmpeg. Docker Compose:

```yaml
services:
  reelyard:
    image: ghcr.io/reelyard/server:latest
    network_mode: host
    restart: unless-stopped
    volumes:
      - reelyard-config:/config
      - /srv/media:/data
volumes:
  reelyard-config:
```

**From source** (Go 1.25+, Node 20+):

```sh
make web && make build
bin/reelyard serve
```

Without `make` (e.g. on Windows): `cd web && npm ci && npm run build && cd ..`, then
`go build -o reelyard.exe ./cmd/reelyard` and `reelyard.exe serve`.

## Running as a service

The packages set this up for you. With a plain binary:

```sh
sudo reelyard install          # system service: systemd (Linux), launchd (macOS)
sudo reelyard start
reelyard install --user        # or a per-user service, no root needed
reelyard start --user
```

On Windows, run `reelyard.exe install` and `reelyard.exe start` from an administrator
terminal. `install` writes a config file into the data folder so the service and the
CLI use the same locations. Other commands: `stop`, `restart`, `status`, `uninstall`,
`reset-password`, `version`.

## Suppliers

Suppliers are Stremio addons. Reelyard uses the ones
that answer with torrent streams (an `infoHash`) and the ones that give subtitles. Add
them in **Suppliers** by pasting the addon's `manifest.json` address (for configurable
addons, configure them on their site first and paste the address it gives you), or pick
one from **Browse addons**. **Test** shows whether an addon answers and how many of its
streams Reelyard can use. Addons that only return debrid or web links aren't usable.

## Watching on a TV (DLNA)

Reelyard has its own DLNA server, on by default. TVs, game consoles and players like VLC on
your network list it as **Reelyard** (rename it in **Settings → DLNA server**) and play straight
from the library, with seeking. Subtitles saved next to a video are offered with it, in your
subtitle-language order (Samsung TVs get them too). Thumbnails need `ffmpeg`, durations
`ffprobe`. Turn on converted versions there for TVs that can't play a file (needs `ffmpeg`).

- It uses TCP port **8200** and SSDP (UDP 1900, multicast); allow both in your firewall.
- Only devices on private networks (192.168.x, 10.x, 172.16–31.x, link-local) can connect.
- It isn't announced on your VPN interface or, unless you choose them, on Docker/VM bridges.

## Jellyfin, Emby and Plex

Point your media server's Movies and Shows libraries at `library/Movies` and `library/TV`.
In **Settings → Jellyfin, Emby and Plex**, add the server's address and an API key (Jellyfin,
Emby) or `X-Plex-Token` (Plex); **Test connection** checks both. Reelyard then asks it to rescan
about 30 seconds after new files arrive (several imports close together share one rescan).
Keys are stored in Reelyard's database and never sent back to the browser.

## Sharing, stalls and disk space

Finished downloads keep sharing until the title's profile ratio or time limit (for downloads
you started by hand: **Settings → Downloads**), whichever comes first. Then sharing stops,
and if the file is in your library its download copy is deleted; the library file stays
(it's a hardlink or a copy). Downloads that make no progress for 30 minutes (adjustable) fail;
tracked titles then try another release. Downloads that wouldn't fit on disk, keeping 2 GB free
(adjustable), are refused.

## Using a VPN

Set `bind_interface` to your VPN's network interface (`wg0`, `tun0`, …) and restart Reelyard.
Every torrent connection — peers, DHT, trackers — is then bound to that interface's address,
so torrent traffic can't leave by any other route. If the interface goes down, torrents pause
and the Downloads page says so; they resume when it's back. If the VPN comes back with a
different address, Reelyard (when running as a service) restarts itself to use it.

## Tracking titles

Open a movie or series and press **Track**. Reelyard then looks for it by itself (every minute
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
and Reelyard picks the next best.

Only download content you have the rights to.

## Folders

| | Linux | macOS | Windows |
|---|---|---|---|
| Data (user) | `~/.local/share/reelyard` | `~/Library/Application Support/Reelyard` | `%ProgramData%\Reelyard` |
| Data (system service) | `/var/lib/reelyard` | `/Library/Application Support/Reelyard` | `%ProgramData%\Reelyard` |

Inside the data folder: `reelyard.db`, `logs/`, `downloads/`, `library/`. The download and
library folders can live anywhere (`download_dir`, `library_dir`).

## Upgrading from Harbor

Reelyard was called Harbor before v0.1.0. An existing install keeps working as it is:
if the Reelyard data folder doesn't exist yet, Reelyard uses the old one (for example
`~/.local/share/harbor`) and its `harbor.db`, and still reads `HARBOR_*` variables. Swap
the service once:

```sh
harbor uninstall --user                # the old binary removes the "harbor" service
reelyard install --user && reelyard start --user
```

`reelyard install` refuses to run while the old service is still installed, so the two
never run on the same data. You'll need to sign in again once.

## Configuration

`config.yaml` in the data folder (or `--config <file>`):

```yaml
listen: ":8787"
data_dir: /srv/reelyard
download_dir: /srv/reelyard/downloads
library_dir: /srv/media
torrent_port: 42069   # peers connect here (TCP+UDP); forward it in your router
bind_interface: wg0   # optional: torrents only use this interface (e.g. your VPN)
log:
  level: info     # debug | info | warn | error
  format: text    # text | json
  file: true      # rotating file in <data>/logs
```

Environment variables override the file: `REELYARD_LISTEN`, `REELYARD_DATA_DIR`,
`REELYARD_DOWNLOAD_DIR`, `REELYARD_LIBRARY_DIR`, `REELYARD_TORRENT_PORT`, `REELYARD_BIND_INTERFACE`, `REELYARD_LOG_LEVEL`,
`REELYARD_LOG_FORMAT`, `REELYARD_LOG_FILE`.

Downloads land in `<download_dir>/<info hash>/…`. When one finishes, Reelyard links it into the
library (a hardlink when both folders are on the same disk, so no extra space is used; a copy
otherwise):

```
Movies/Night of the Living Dead (1968)/Night of the Living Dead (1968) - 720p.mp4
Movies/Night of the Living Dead (1968)/Night of the Living Dead (1968) - 720p.en.srt
TV/Breaking Bad (2008)/Season 01/Breaking Bad (2008) - S01E02 - Cat's in the Bag - 1080p.mkv
```

Subtitles come from your subtitle suppliers (e.g. OpenSubtitles) for the languages chosen in
**Settings → Library**. If `ffprobe` (from FFmpeg) is installed, Reelyard uses it to read the real
resolution and to skip languages already built into the video. Speed limits and how many downloads run at
once are set in **Settings → Downloads**.

## Forgot the password?

```sh
reelyard reset-password                    # prompts for a new password
echo 'new password' | reelyard reset-password --password-stdin
```

## API

Everything the UI does goes through `/api/v1`. Scripts can use an API key from
**Settings → API keys**, sent as the `X-Api-Key` header:

```sh
curl -H "X-Api-Key: hbr_..." http://localhost:8787/api/v1/system
```

`Authorization: Bearer hbr_...` works too.

### Apps and devices

Reelyard's apps for TVs, phones and computers ([Reelyard/clients](https://github.com/Reelyard/clients))
sign in by pairing code instead of a password:

1. The app calls `POST /api/v1/devices/pair` with `{"name": "Living room TV", "platform": "tizen"}`
   and shows the `code` it gets back (e.g. `K7Q-4MD`).
2. You enter that code under **Settings → Devices**.
3. The app, polling `POST /api/v1/devices/pair/token` with its `pollSecret`, gets its own token
   (`hbd_...`) and sends it as `Authorization: Bearer hbd_...`.

Codes work for 10 minutes. **Settings → Devices** lists every device and signs any of them out.

To play a file, an app asks `POST /api/v1/library/{id}/play` for a link to `/api/v1/play/{id}`
that works for 8 hours without any header, so any video player can open it and seek.
`GET /api/v1/library/titles` lists the library by movie and series;
`GET /api/v1/library/titles/{imdbId}` gives a title's seasons, episodes and files.
Apps on another origin (Samsung and LG TV apps) can call the API: it answers CORS requests
that carry a token, never with cookies.

## Development

```sh
go run ./cmd/reelyard serve      # API on :8787
cd web && npm run dev          # UI on :5173 with hot reload, /api proxied to :8787
make test vet
make cross                     # all six OS/arch binaries into dist/
```

Layout: `cmd/reelyard` (CLI), `internal/config`, `internal/logging`, `internal/store`
(SQLite + migrations), `internal/auth`, `internal/api` (HTTP + embedded UI),
`internal/app` (lifecycle), `web/` (React + Vite + Tailwind, embedded with `go:embed`).

## License

MIT — see [LICENSE](LICENSE).
