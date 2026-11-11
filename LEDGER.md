# Harbor — Project Ledger

Tracks progress against [PLAN.md](PLAN.md). Update this file whenever a task changes state,
a decision is made, or a work session ends.

**Status key:** `[ ]` todo · `[~]` in progress · `[x]` done · `[!]` blocked · `[-]` dropped

**Current milestone:** M0 — Skeleton (not started)
**Last updated:** 2026-11-11

---

## 1. Milestone overview

| # | Milestone | Status | Tasks done | Started | Finished |
|---|---|---|---|---|---|
| M0 | Skeleton | `[ ]` | 0 / 10 | | |
| M1 | Suppliers + metadata | `[ ]` | 0 / 9 | | |
| M2 | Download engine | `[ ]` | 0 / 8 | | |
| M3 | Import + subtitles | `[ ]` | 0 / 9 | | |
| M4 | Automation | `[ ]` | 0 / 9 | | |
| M5 | Seeding + robustness | `[ ]` | 0 / 7 | | |
| M6 | DLNA + media servers | `[ ]` | 0 / 6 | | |
| M7 | Packaging | `[ ]` | 0 / 8 | | |

---

## 2. Tasks

### M0 — Skeleton
Done when: binary starts on Linux/macOS/Windows, service install works, SQLite + migrations,
login, empty UI, CI builds all targets.

- [ ] M0-01 Create Go module, repo layout (`cmd/`, `internal/`, `web/`, `packaging/`), README, LICENSE
- [ ] M0-02 Config loader: YAML file + env vars, default data paths per OS
- [ ] M0-03 Logging with `log/slog` (text/JSON), rotating log file
- [ ] M0-04 SQLite store (`modernc.org/sqlite`) + embedded migration runner + initial schema
- [ ] M0-05 HTTP server (stdlib routing), health endpoint, graceful shutdown
- [ ] M0-06 Auth: admin user (bcrypt), session cookie, API key, `reset-password` command
- [ ] M0-07 React + Vite + Tailwind app shell (login, nav, empty pages), embedded with `go:embed`
- [ ] M0-08 CLI: `serve`, `install`, `uninstall`, `start`, `stop`, `version` via `kardianos/service`
- [ ] M0-09 GitHub Actions: lint, test, build matrix (linux/darwin/windows × amd64/arm64)
- [ ] M0-10 Smoke test: run as service on Linux, macOS, Windows (record results in log)

### M1 — Suppliers + metadata
Done when: add/test addon manifests, search titles in Cinemeta, manual search lists parsed candidates.

- [ ] M1-01 Stremio addon client: manifest fetch + capability parsing (resources, types, idPrefixes)
- [ ] M1-02 Stream endpoint client (movie + `tt:S:E` episode IDs), timeouts, per-supplier errors
- [ ] M1-03 Subtitle endpoint client (with `videoHash`/`videoSize`/`filename` extras)
- [ ] M1-04 Suppliers CRUD API + "Test" action (flags debrid-only/no-infoHash suppliers)
- [ ] M1-05 Cinemeta client: search, movie meta, series meta with episode list
- [ ] M1-06 Release-name parser (resolution, source, codec, HDR, audio, languages, S/E, packs, size, seeders)
- [ ] M1-07 Parser fixtures from real addon responses + table tests
- [ ] M1-08 UI: Suppliers page, Add title search page
- [ ] M1-09 UI: Manual search page listing candidates with parsed fields

### M2 — Download engine
Done when: grab a candidate, download only the needed file, live progress in UI, resume after restart.

- [ ] M2-01 Engine wrapper around `anacrolix/torrent` (client config, listen port, storage dir)
- [ ] M2-02 Add by infoHash + trackers/DHT sources; select single file by `fileIdx`
- [ ] M2-03 Download/upload speed limits, max active downloads queue
- [ ] M2-04 Persist download state; resume on restart (piece completion DB)
- [ ] M2-05 Event bus + SSE stream for live progress
- [ ] M2-06 Downloads API: list, pause, resume, remove (with/without data)
- [ ] M2-07 UI: Downloads page + dashboard widget (speed, ETA, peers)
- [ ] M2-08 Grab action from manual search wired end to end

### M3 — Import + subtitles
Done when: file renamed into library via hardlink/copy; subtitles in wanted languages saved next to it.

- [ ] M3-01 Naming templates (Jellyfin/Plex) for movies and episodes, name sanitizing for Windows
- [ ] M3-02 Hardlink with copy fallback (cross-device, FAT/exFAT), Windows long paths
- [ ] M3-03 Optional `ffprobe` probe: real resolution, audio langs, embedded subtitle langs
- [ ] M3-04 OpenSubtitles hash (size + first/last 64 KB)
- [ ] M3-05 Subtitle fetch from subtitle suppliers, choose best per language
- [ ] M3-06 VTT → SRT conversion, save as `name.<lang>.srt`
- [ ] M3-07 `media_files` / `subtitle_files` records, title/episode status → imported
- [ ] M3-08 Activity log entries for import and subtitle results
- [ ] M3-09 UI: Title detail shows files and subtitles

### M4 — Automation
Done when: profiles + scorer; scheduler auto-grabs wanted movies and aired episodes; season-pack logic.

- [ ] M4-01 Quality profiles CRUD (resolutions, sizes, audio/sub langs, terms, min seeders)
- [ ] M4-02 Scorer: hard filters with rejection reasons, ranking
- [ ] M4-03 Scheduler with intervals + backoff per item
- [ ] M4-04 Movie search job: wanted → best candidate → grab
- [ ] M4-05 Series metadata refresh (daily), episode rows, aired → wanted
- [ ] M4-06 Monitor modes: all, future only, selected seasons, paused
- [ ] M4-07 Season-pack preference when several episodes of a season are missing
- [ ] M4-08 UI: Profiles page, Titles grid with status filters, Title detail episodes
- [ ] M4-09 Language check after probe: required language missing → fail + next candidate

### M5 — Seeding + robustness
Done when: ratio/time stop and cleanup; stall detection + blocklist + retry; VPN interface binding.

- [ ] M5-01 Seed manager: stop at ratio or seed time (global + per profile)
- [ ] M5-02 Cleanup: remove torrent + `downloads/` data, keep library file
- [ ] M5-03 Stall detection (no progress for N minutes) → blocklist hash → next candidate
- [ ] M5-04 Blocklist API + UI
- [ ] M5-05 Bind torrent traffic to a network interface/IP; pause if it goes down
- [ ] M5-06 Disk space check before grabbing
- [ ] M5-07 Failure handling review (supplier down, metadata down, corrupt file)

### M6 — DLNA + media servers
Done when: built-in DLNA visible on TVs; Jellyfin/Plex/Emby refresh hooks.

- [ ] M6-01 Embed `anacrolix/dms` serving the library root
- [ ] M6-02 DLNA settings: on/off, friendly name, interface, optional ffmpeg transcoding
- [ ] M6-03 Expose `.srt` subtitles to DLNA clients
- [ ] M6-04 Jellyfin / Emby refresh API hook
- [ ] M6-05 Plex refresh API hook
- [ ] M6-06 Test on real TVs/players (record which devices work)

### M7 — Packaging
Done when: GoReleaser packages, Docker image, first-run wizard, docs.

- [ ] M7-01 `.goreleaser.yaml`: archives for all targets
- [ ] M7-02 `.deb` / `.rpm` with systemd unit
- [ ] M7-03 Homebrew tap + launchd plist
- [ ] M7-04 Windows zip + service install notes (MSI/winget later)
- [ ] M7-05 Dockerfile + image (document `--network host` for DLNA)
- [ ] M7-06 First-run wizard: admin account, folders, first supplier
- [ ] M7-07 User docs: install, suppliers, profiles, VPN, DLNA
- [ ] M7-08 Tag v0.1.0 release

---

## 3. Decisions

| ID | Date | Decision | Reason |
|---|---|---|---|
| D-01 | 2026-11-11 | Built-in torrent engine (`anacrolix/torrent`) | Single binary, nothing else to install |
| D-02 | 2026-11-11 | Track movies + TV series | Series follow new episodes as they air |
| D-03 | 2026-11-11 | Seed until ratio or time limit, then clean up | Fair sharing without filling the disk |
| D-04 | 2026-11-11 | Jellyfin/Plex naming **and** built-in DLNA server | Works with media servers and directly with TVs |
| D-05 | 2026-11-11 | Linux, macOS, Windows; amd64 + arm64; system service | User requirement |
| D-06 | 2026-11-11 | Pure-Go SQLite (`modernc.org/sqlite`), no CGO | Easy cross-compiling |
| D-07 | 2026-11-11 | Cinemeta for metadata (TMDB later) | No API key needed |
| D-08 | 2026-11-11 | Only streams with `infoHash` are used | Debrid/HTTP streams out of scope for v1 |

---

## 4. Open questions

| ID | Question | Default if no answer | Status |
|---|---|---|---|
| Q-01 | Project name? | "Harbor" | open |
| Q-02 | UI stack: React + Tailwind, or Go templates + htmx? | React + Tailwind | open |
| Q-03 | Add TMDB as a metadata source? | Later | open |
| Q-04 | Which notifications (Telegram, ntfy, Discord)? | After M7 | open |
| Q-05 | Git host for repo + CI (GitHub?) | GitHub | open |
| Q-06 | Project folder location on disk | — | open |

---

## 5. Risks

| ID | Risk | Mitigation | Status |
|---|---|---|---|
| R-01 | Addon release text formats differ | Fixtures per supplier + parser adapters | open |
| R-02 | Language detection from names unreliable | `ffprobe` check after download | open |
| R-03 | Some configured addons return only debrid URLs | Supplier test flags them | open |
| R-04 | Windows paths/locks | Sanitizing, `\\?\` long paths, hardlinks | open |
| R-05 | DLNA multicast blocked (Docker, firewalls) | Host networking, docs | open |
| R-06 | Torrent traffic visible to ISP | VPN interface binding (M5-05) | open |

---

## 6. Work log

| Date | Session summary | Tasks touched |
|---|---|---|
| 2026-11-11 | Requirements gathered, PLAN.md and LEDGER.md created | — |
