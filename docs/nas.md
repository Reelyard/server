# Installing Reelyard on a NAS

Reelyard runs on any NAS that runs Docker containers, using the image
`ghcr.io/reelyard/server`. On every system the same three things matter:

- **One media folder for downloads and library.** Mount it at `/data` (or
  `/DATA` on ZimaOS). Keeping both on one share lets Reelyard hardlink finished
  files into the library instead of copying them.
- **Host networking** (`network_mode: host`), so TVs can find the DLNA
  server. Without DLNA you can publish ports 8787 (web UI) and 42069 TCP+UDP
  (torrents) instead.
- **PUID / PGID**: the user and group that should own downloaded files.
  `UMASK` sets permissions for new files (default `002`).

After installing, open `http://<nas>:8787`. The first visit creates your
account. Point Jellyfin or Plex at the library folder, or open **Reelyard**
in your TV's media menu. Only download content you have the rights to.

The generic Compose file is [`deploy/compose.yaml`](../deploy/compose.yaml).

## ZimaOS and CasaOS

Reelyard has its own app store for ZimaOS and CasaOS (0.4.4 or newer).

1. Open the **App Store**, choose to add a source (the **+** / **App source**
   menu), and paste:
   `https://reelyard.github.io/server/zimaos/`
2. Install **Reelyard** from that source.

It uses `/DATA/AppData/reelyard/config` for its settings, keeps downloads in
`/DATA/Downloads/Reelyard`, and adds `Movies/` and `TV/` to `/DATA/Media`,
where ZimaOS's Jellyfin and Plex apps already look. Your ZimaOS user's
PUID/PGID and time zone are filled in for you.

Without the store: **App Store → Custom install → Import**, and paste
[`deploy/zimaos/Apps/Reelyard/docker-compose.yml`](../deploy/zimaos/Apps/Reelyard/docker-compose.yml).

## Unraid

1. **Docker** tab → **Add Container** → paste this into **Template**:
   `https://raw.githubusercontent.com/reelyard/server/main/deploy/unraid/reelyard.xml`
   (or add it under **Template repositories** in Docker settings).
2. Check the two paths: **Config** (`/mnt/user/appdata/reelyard`) and
   **Media** (`/mnt/user/data/reelyard`, which will hold `downloads/` and
   `library/`), then **Apply**.

PUID 99 / PGID 100 and UMASK 000 match Unraid's share permissions.

## TrueNAS SCALE (24.10 and newer)

1. Create datasets, for example `tank/apps/reelyard` and `tank/media`.
2. **Apps → Discover Apps → ⋮ → Install via YAML**, name it `reelyard`, and
   paste:

   ```yaml
   services:
     reelyard:
       image: ghcr.io/reelyard/server:latest
       restart: unless-stopped
       network_mode: host
       environment:
         PUID: "568"        # TrueNAS's "apps" user
         PGID: "568"
         TZ: Etc/UTC
       volumes:
         - /mnt/tank/apps/reelyard:/config
         - /mnt/tank/media:/data
   ```

TrueNAS 25.10 requires the top-level `services:` key, as above. Give the
`apps` user (568) write access to the media dataset, or use your own user's
ids.

## Synology DSM 7.2+

1. Install **Container Manager** from Package Center.
2. In **File Station**, create `docker/reelyard` and a media folder such as
   `media/reelyard`.
3. **Container Manager → Project → Create**, path `docker/reelyard`, choose
   **Create docker-compose.yml**, and paste [`deploy/compose.yaml`](../deploy/compose.yaml)
   with the volumes changed to `/volume1/docker/reelyard:/config` and
   `/volume1/media/reelyard:/data`.
4. Set PUID/PGID to your DSM user (SSH: `id <user>`; usually 1026 / 100).

## QNAP (Container Station 3)

**Container Station → Applications → Create**, name it `reelyard`, paste
[`deploy/compose.yaml`](../deploy/compose.yaml) with the volumes changed to
your shares (for example `/share/Container/reelyard:/config` and
`/share/Multimedia/reelyard:/data`), and set PUID/PGID to your user
(`id <user>` over SSH).

## OpenMediaVault 7

Install the **openmediavault-compose** plugin, then **Services → Compose →
Files → Add**, paste [`deploy/compose.yaml`](../deploy/compose.yaml) with your
shared folders' paths under `/srv/...`, save, and press **Up**.

## Any Linux machine with Docker

```sh
mkdir reelyard && cd reelyard
curl -LO https://raw.githubusercontent.com/reelyard/server/main/deploy/compose.yaml
# edit the paths, PUID/PGID and TZ, then:
docker compose up -d
```

## Updating

Pull the new image and recreate the container (on most NAS systems, the app
or project has an **Update** or **Pull** action). Your settings live in the
config folder and survive updates.
