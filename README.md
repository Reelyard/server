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

Website and docs: <https://reelyard.github.io/server/> (source in [`site/`](site), published by
the Website workflow).

## Install

Downloads are on the [releases page](https://github.com/reelyard/server/releases). The
**[install guide](docs/install.md)** covers installing, updating and uninstalling on each
system, where your data lives, and the ports to open.

| You have… | Install |
|---|---|
| A NAS (ZimaOS/CasaOS, Unraid, TrueNAS, Synology, QNAP, OpenMediaVault) | Docker image: [NAS guide](docs/nas.md) |
| A Raspberry Pi 4 / 5 | Ready-made image: [Raspberry Pi guide](docs/raspberry-pi.md) |
| Docker on Linux | `ghcr.io/reelyard/server`: [Docker](docs/install.md#docker) |
| Debian, Ubuntu, Fedora, RHEL, openSUSE | `.deb` / `.rpm`: [Linux packages](docs/install.md#linux-deb-and-rpm) |
| Windows, macOS, other Linux | Archive with `INSTALL.txt`: [install guide](docs/install.md#windows) |

Quickest start with Docker:

```sh
docker run -d --name reelyard --network host --restart unless-stopped \
  -e PUID=1000 -e PGID=1000 \
  -v reelyard-config:/config -v /srv/media:/data \
  ghcr.io/reelyard/server:latest
```

Then open <http://localhost:8787>: the first visit creates your account and walks you
through folders and suppliers. Building from source: see
[Development](#development) and the [install guide](docs/install.md#from-source).

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

### Viewers: a server per family member

TVs don't sign in, so each person who watches gets their own server in the TV's sources:
**Settings → Viewers → Add viewer** creates, say, "Reelyard · Kids" next to the main
"Reelyard". On a viewer's server:

- watched marks and resume points are theirs, and a **Continue watching** folder at the top
  lists what they're halfway through (the main server has one for your account too);
- **Only titles I choose** limits it to the films and series you tick: other folders don't
  appear, and their files can't be opened from that server;
- **Show on these TVs** shows it only on the TVs you tick (the main server has the same
  option). TVs are listed once they've opened any Reelyard server, and are told apart by
  address, so give each a fixed IP in your router.

Each viewer's server uses the next port after the main one (8201, 8202, …); allow those in
your firewall too. Anyone using a TV that shows a server can open it: hide the main server
from a child's TV, and don't rely on this to keep someone determined out.

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

Only download content you have the rights to (see [Legal notice](#legal-notice)).

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
announce: true        # apps on the local network find Reelyard by multicast DNS
log:
  level: info     # debug | info | warn | error
  format: text    # text | json
  file: true      # rotating file in <data>/logs
```

Environment variables override the file: `REELYARD_LISTEN`, `REELYARD_DATA_DIR`,
`REELYARD_DOWNLOAD_DIR`, `REELYARD_LIBRARY_DIR`, `REELYARD_TORRENT_PORT`, `REELYARD_BIND_INTERFACE`, `REELYARD_LOG_LEVEL`,
`REELYARD_LOG_FORMAT`, `REELYARD_LOG_FILE`, `REELYARD_ANNOUNCE`.

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
that works for 8 hours without any header, so any video player can open it and seek. The answer
also carries the file's subtitles as WebVTT links, where the user stopped last time, and — when
the app says what its player can play — whether this file will play and why not.
`GET /api/v1/home` gives the home screen (continue watching, next episode, recently added);
`PUT /api/v1/progress/{itemId}` saves where the user is. `GET /api/v1/library/titles` lists the
library by movie and series, with artwork served (and cached) by Reelyard;
`GET /api/v1/library/titles/{imdbId}` gives a title's seasons, episodes and files.
Apps on another origin (Samsung and LG TV apps) can call the API: it answers CORS requests
that carry a token, never with cookies.

Apps find Reelyard on the local network by multicast DNS (service `_reelyard._tcp`); set
`announce: false` (or `REELYARD_ANNOUNCE=0`) to stop that. Reelyard doesn't announce itself when
it only listens on this machine. The whole app API is described in OpenAPI at
`/api/v1/openapi.yaml`.

## Development

```sh
go run ./cmd/reelyard serve      # API on :8787
cd web && npm run dev          # UI on :5173 with hot reload, /api proxied to :8787
make test vet
make ci                        # the CI checks locally: web build, gofmt/vet, tests, smoke test
make hooks                     # run `make ci` before every git push (skip with --no-verify)
make cross                     # all six OS/arch binaries into dist/
```

Layout: `cmd/reelyard` (CLI), `internal/config`, `internal/logging`, `internal/store`
(SQLite + migrations), `internal/auth`, `internal/api` (HTTP + embedded UI),
`internal/app` (lifecycle), `web/` (React + Vite + Tailwind, embedded with `go:embed`).

## Legal notice

**Reelyard does not condone piracy.** Reelyard is software you run on your own machine. We do
not host, store, index, link to or distribute any movies, series or other media, legal or
illegal, and this repository, our releases, Docker images, app stores and website contain none.
Reelyard ships with no content sources: it only finds what the third-party addons you choose to
add give it, and we neither run nor endorse any addon.

You decide what Reelyard downloads and shares, and you are responsible for it. Torrents share
what you download with others while they run. Only download and share content you have the
rights to (public-domain or openly licensed works, for example), and follow the laws where you
live. If you believe something in this repository infringes your rights, open an issue.

## License

MIT — see [LICENSE](LICENSE).
