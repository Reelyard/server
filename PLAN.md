# Harbor — Stremio-supplier media tracker & downloader (Go)

> Working name: **Harbor**. A single Go binary for Linux, macOS and Windows.
> It watches movies and TV series, finds torrents through Stremio addons ("suppliers"),
> downloads them with a built-in torrent engine, puts them in a media library with
> Jellyfin/Plex naming plus subtitles, and serves that library over DLNA.

## 1. Decisions taken

| Topic | Decision |
|---|---|
| Torrent engine | Built-in (`github.com/anacrolix/torrent`), no external client |
| Content | Movies (download once) + TV series (follow new episodes) |
| After download | Seed until a ratio **or** time limit, then stop and clean up |
| Playback | Library folder with Jellyfin/Plex/Emby naming **and** a built-in DLNA server |
| Platforms | Linux, macOS, Windows (amd64 + arm64), installed as a system service |

## 2. How it works

```
          ┌──────────── Web UI (embedded SPA) ────────────┐
          │ Suppliers · Titles · Profiles · Downloads ·   │
          │ Settings · Activity                           │
          └──────────────────┬────────────────────────────┘
                             │ REST /api/v1 + SSE (live progress)
┌────────────────────────────┴──────────────────────────────────┐
│ Harbor (Go)                                                   │
│                                                               │
│  Scheduler ──► Searcher ──► Stremio addon client ──► suppliers│
│      │            │                (stream / subtitles)       │
│      │            ▼                                           │
│      │       Release parser + Profile scorer ──► best pick    │
│      │            │                                           │
│      ▼            ▼                                           │
│  Metadata     Torrent engine (anacrolix) ──► downloads/       │
│  (Cinemeta)       │ on complete                               │
│                   ▼                                           │
│               Importer ──► library/ (hardlink or copy, rename)│
│                   │                                           │
│                   ├──► Subtitle fetcher (Stremio subs addons) │
│                   ├──► Jellyfin/Plex refresh (optional)       │
│                   └──► Seed manager (ratio/time → stop, purge)│
│                                                               │
│  DLNA server (anacrolix/dms) serves library/                  │
│  SQLite store (modernc.org/sqlite, pure Go)                   │
└───────────────────────────────────────────────────────────────┘
```

### 2.1 Suppliers = Stremio addons
A supplier is a Stremio addon **manifest URL** (configured addons have their config in the
path, e.g. `https://host/<config>/manifest.json`). Harbor reads:

- `GET {base}/manifest.json` → name, `resources` (`stream`, `subtitles`, `meta`, `catalog`),
  `types` (`movie`, `series`), `idPrefixes` (`tt`). The UI shows what each supplier can do.
- `GET {base}/stream/{type}/{id}.json` where `id` is `tt1234567` (movie) or
  `tt1234567:S:E` (episode). Each stream may have `infoHash`, `fileIdx`, `sources`
  (trackers / `dht:`), `title`/`description` (release name, size, seeders, language flags),
  and `behaviorHints.filename` / `videoSize`.
  **Only streams with an `infoHash` are usable**; plain `url` streams (debrid/HTTP) are
  ignored for now (possible later feature).
- `GET {base}/subtitles/{type}/{id}/videoHash=…&videoSize=…&filename=….json` →
  `[{id, url, lang}]` for subtitle suppliers (e.g. an OpenSubtitles addon).

Each supplier has: enabled flag, priority, timeout, last success / last error, and a
"Test" button that queries a known title.

### 2.2 Metadata (titles & episodes)
Default: **Cinemeta** (Stremio's public metadata addon, no API key):
- search: `/catalog/{movie|series}/top/search={query}.json`
- details: `/meta/{type}/{imdbId}.json` → name, year, poster, and for series the `videos`
  list (season, episode, title, release date) used to know which episodes have aired.

Optional later: TMDB as a second source (needs an API key).

### 2.3 Choosing a release (quality profiles)
A **quality profile** is attached to every title:
- allowed resolutions in preference order (e.g. 2160p > 1080p > 720p)
- min / max size (per movie, per episode)
- preferred **audio languages** (e.g. `pt`, `en`), with "required" or "preferred"
- **subtitle languages** wanted (e.g. `pt`, `en`) and whether a release without them is acceptable
- blocked terms (`CAM`, `TS`, `HDCAM`, `3D`…), preferred terms (`REMUX`, `WEB-DL`, `x265`…)
- minimum seeders

The **release parser** turns a torrent name + addon description into fields:
resolution, source, codec, HDR/DV, audio, languages (from words like `MULTI`, `PT-BR`,
`ENG`, `DUAL` and from flag emojis), season/episode or season pack, size, seeders.
It has a generic regex table plus small adapters for common addon formats.

The **scorer** filters out anything that fails hard rules, then ranks the rest
(resolution rank → language match → preferred terms → seeders → size closeness).
The UI "manual search" shows every candidate with its score and the reason for each
rejection, so you can pick one by hand.

### 2.4 Downloading
- `anacrolix/torrent` client, file storage in `downloads/`.
- Only the needed file is downloaded (`fileIdx`), which also makes season packs work for
  a single episode; if several episodes of one season are missing, a season pack is preferred.
- Settings: max active downloads, download/upload speed limits, listen port, and
  **bind to a network interface/IP** (so you can force traffic through a VPN; if that
  interface goes down, downloads pause instead of using the normal connection).
- Stall detection: no progress for N minutes → mark the hash as bad, try the next candidate.
- Torrent state survives restarts (resume from the SQLite record + piece completion DB).

### 2.5 Import into the library
When the file is complete:
1. Verify size, optionally run `ffprobe` (if installed) to read real resolution,
   audio and embedded subtitle languages.
2. Hardlink into the library (same disk, no extra space, seeding keeps working);
   copy if hardlinks are not possible (different disk, FAT/exFAT).
3. Name it for Jellyfin/Plex:
   - `Movies/Title (Year)/Title (Year) - 1080p.mkv`
   - `TV/Show (Year)/Season 01/Show (Year) - S01E02 - Episode Name - 1080p.mkv`
   - Unsafe characters (`: ? * " < > |`) removed so names work on Windows.
4. Subtitles: for each wanted language not already embedded, ask subtitle suppliers using
   the OpenSubtitles file hash + size + filename; save as
   `... .pt.srt`, `... .en.srt` (VTT converted to SRT).
5. Optional: call the Jellyfin/Plex/Emby "refresh library" API.

### 2.6 Seeding
Per profile or global: stop when **ratio ≥ X** or **seeded time ≥ Y**, whichever first.
Then the torrent is removed and its `downloads/` copy deleted (the library hardlink/copy stays).

### 2.7 DLNA
Built-in DLNA/UPnP media server using `github.com/anacrolix/dms`, serving the library folder.
Settings: on/off, friendly name, network interface, optional ffmpeg transcoding.
Subtitle `.srt` files are exposed next to videos for TVs that support them.

### 2.8 Series tracking
Monitor modes: all episodes, future episodes only, selected seasons, none (paused).
Metadata refreshes daily; an episode becomes "wanted" once its release date has passed.
Status per item: `missing → searching → downloading → imported` (or `failed`, with reason).

## 3. Web UI
Embedded in the binary (`go:embed`), opened at `http://<host>:8787`.

| Page | What it does |
|---|---|
| Dashboard | Active downloads (speed, ETA, peers), recent imports, failures, supplier health |
| Titles | Poster grid of tracked movies/series with status filters |
| Add title | Search Cinemeta, pick profile, monitor mode, root folder |
| Title detail | Episodes per season, status, manual search with all candidates, history |
| Suppliers | Add manifest URL, see capabilities, enable/priority, test |
| Quality profiles | Resolution order, sizes, audio/subtitle languages, terms, seeding rules |
| Downloads | Pause / resume / remove, ratio, seeding time |
| Settings | Paths, schedule, speed limits, VPN interface, DLNA, media server hooks, account |
| Activity | Event log (searched, grabbed, imported, failed) |

Tech: React + TypeScript + Vite + Tailwind (shadcn/ui components), live updates over
Server-Sent Events. Login with one admin account (bcrypt, session cookie) and an API key
for scripts.

## 4. Tech stack (Go)

| Need | Package |
|---|---|
| HTTP routing | Go stdlib `net/http` (method + path patterns) |
| Database | SQLite via `modernc.org/sqlite` (pure Go → easy cross-compile, no CGO) |
| Migrations | embedded SQL files, small custom runner (or `pressly/goose`) |
| Torrents | `github.com/anacrolix/torrent` |
| DLNA | `github.com/anacrolix/dms` |
| OS service | `github.com/kardianos/service` (systemd, launchd, Windows Service) |
| Config | YAML file + env vars; most settings editable in the UI (stored in DB) |
| Logging | `log/slog` (JSON or text), rotating file |
| Releases | GoReleaser + GitHub Actions |

## 5. Project layout

```
harbor/
  cmd/harbor/main.go          # CLI: serve | install | uninstall | start | stop | reset-password | version
  internal/
    config/                   # file/env config, default paths per OS
    store/                    # SQLite access + migrations/
    stremio/                  # addon client: manifest, streams, subtitles
    metadata/                 # Cinemeta client (TMDB later)
    release/                  # torrent-name parser
    profile/                  # filtering + scoring
    search/                   # search jobs: title → candidates → pick
    engine/                   # anacrolix wrapper: add, select file, limits, stats
    importer/                 # naming, hardlink/copy, ffprobe
    subtitles/                # OpenSubtitles hash, fetch, VTT→SRT
    seeding/                  # ratio/time policy, cleanup
    scheduler/                # periodic jobs with backoff
    dlna/                     # dms setup
    mediaserver/              # Jellyfin/Plex/Emby refresh hooks
    api/                      # REST handlers, SSE, auth middleware
    events/                   # in-process event bus (UI + activity log)
  web/                        # React app → built to web/dist, embedded
  packaging/                  # systemd unit, launchd plist, Windows notes, Dockerfile
  .goreleaser.yaml
```

Default data locations (overridable):
- Linux: `/var/lib/harbor` (service) or `~/.local/share/harbor`
- macOS: `~/Library/Application Support/Harbor`
- Windows: `%ProgramData%\Harbor`

## 6. Data model (SQLite)

- `suppliers` (id, name, manifest_url, resources, types, enabled, priority, last_ok_at, last_error)
- `profiles` (id, name, resolutions, min/max size, audio_langs, sub_langs, subs_required,
  blocked_terms, preferred_terms, min_seeders, seed_ratio, seed_hours)
- `titles` (id, imdb_id, type, name, year, poster, profile_id, monitor_mode, root_folder, status, added_at)
- `episodes` (id, title_id, season, episode, name, air_date, monitored, status)
- `candidates` (id, item_kind, item_id, supplier_id, info_hash, file_idx, raw_name, parsed_json,
  size, seeders, score, rejected_reason, seen_at)
- `blocklist` (info_hash, item, reason, created_at)
- `downloads` (id, item_kind, item_id, info_hash, file_idx, state, progress, downloaded, uploaded,
  started_at, completed_at, seed_until)
- `media_files` (id, item_kind, item_id, path, size, resolution, audio_langs, embedded_subs)
- `subtitle_files` (id, media_file_id, lang, source_supplier, path)
- `events` (id, time, level, kind, item, message)
- `settings` (key, value) · `users` (id, username, password_hash) · `api_keys`

## 7. REST API (v1, summary)

```
POST   /api/v1/auth/login | /logout
GET    /api/v1/suppliers            POST   /api/v1/suppliers
PATCH  /api/v1/suppliers/{id}       DELETE /api/v1/suppliers/{id}
POST   /api/v1/suppliers/{id}/test
GET    /api/v1/metadata/search?q=&type=
GET    /api/v1/titles               POST   /api/v1/titles
GET    /api/v1/titles/{id}          PATCH/DELETE /api/v1/titles/{id}
POST   /api/v1/titles/{id}/search           # automatic search now
GET    /api/v1/items/{kind}/{id}/candidates  # manual search results
POST   /api/v1/items/{kind}/{id}/grab        # download a chosen candidate
GET    /api/v1/downloads            POST /api/v1/downloads/{id}/pause|resume  DELETE
GET    /api/v1/profiles  (+ CRUD)
GET    /api/v1/settings             PUT  /api/v1/settings
GET    /api/v1/events?since=        GET  /api/v1/stream  (SSE)
```

## 8. Install & distribution

- `harbor install` registers the service (systemd / launchd / Windows Service) and
  `harbor start` runs it; `harbor serve` runs in the foreground.
- GoReleaser builds `linux|darwin|windows × amd64|arm64`:
  `.tar.gz`/`.zip`, `.deb`/`.rpm`, Homebrew tap, Windows zip (MSI/winget later),
  Docker image (`ghcr.io/...`) for NAS users.
- First run opens a setup page: create admin account, choose download + library folders,
  add the first supplier.
- macOS: unsigned binaries need `xattr -d com.apple.quarantine` or Homebrew;
  signing/notarization later.
- Windows: firewall prompt for torrent port and DLNA (SSDP, UDP 1900) on first run.

## 9. Milestones

| # | Goal | Done when |
|---|---|---|
| M0 | Skeleton | Binary starts on all 3 OSes, service install works, SQLite + migrations, login, empty UI, CI builds all targets |
| M1 | Suppliers + metadata | Add/test addon manifests; search titles in Cinemeta; manual search lists parsed candidates |
| M2 | Download engine | Grab a candidate, download only the needed file, live progress in UI, resumes after restart |
| M3 | Import + subtitles | File renamed into library via hardlink/copy; subtitles in wanted languages saved next to it |
| M4 | Automation | Profiles + scorer; scheduler auto-grabs wanted movies and aired episodes; season-pack logic |
| M5 | Seeding + robustness | Ratio/time stop and cleanup; stall detection + blocklist + retry; VPN interface binding |
| M6 | DLNA + media servers | Built-in DLNA visible on TVs; Jellyfin/Plex/Emby refresh hooks |
| M7 | Packaging | GoReleaser packages, Docker image, first-run wizard, docs |

Later ideas: quality upgrades (replace 720p with 1080p when found), notifications
(Telegram, ntfy, Discord), HTTP/debrid streams from addons, multi-user, import of an existing library.

## 10. Risks & notes

- **Legal:** Harbor is a neutral tool; only download content you have the rights to.
  Torrent traffic is visible to your ISP — the VPN interface binding option is there for that.
- **Addon formats differ:** release info lives in free text; the parser needs tests built from
  real responses (saved as fixtures) for each supplier you use.
- **Language detection from names is unreliable:** confirm with `ffprobe` after download when
  available, and treat a mismatch as failed (try the next candidate) if the language is required.
- **Some configured addons return only debrid URLs** (no `infoHash`); Harbor will show them as
  "not usable" in the supplier test.
- **Windows:** long paths (use `\\?\` prefixes), name sanitizing, and file locks while seeding
  (avoided by hardlinks).
- **DLNA discovery** needs multicast on the LAN; Docker needs `--network host` for it.
