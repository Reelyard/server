# Reelyard beta testing

Thanks for helping test Reelyard before its first release. Anyone can join:
betas are published on GitHub like any release, and problems are reported as
GitHub issues. Expect rough edges, and keep a copy of anything you can't
lose.

Only download content you have the rights to. Public-domain films such as
*Night of the Living Dead* (1968) are good for testing.

## Install the beta

Beta releases are tagged `v0.1.0-beta.N` and marked **Pre-release** under
[Releases](https://github.com/reelyard/server/releases).

**Docker and NAS systems** use the image `ghcr.io/reelyard/server:beta`, which
always points at the newest beta. Follow the [NAS guide](nas.md) for your
system, with these changes:

- **Any Compose file** (Docker, TrueNAS, Synology, QNAP, OpenMediaVault):
  change the image line to `image: ghcr.io/reelyard/server:beta`.
- **ZimaOS / CasaOS**: add the beta app store,
  `https://reelyard.github.io/server/zimaos-beta/`, as an app source (see
  the [NAS guide](nas.md#zimaos-and-casaos)) and install **Reelyard (beta)**.
  It follows `ghcr.io/reelyard/server:beta`, so it is a separate app from
  the stable **Reelyard** in `…/server/zimaos/`.
- **Unraid**: add the template as the guide says, then set **Repository** to
  `ghcr.io/reelyard/server:beta`.

**Raspberry Pi 4 / 5**: download `reelyard-pi4.img.xz` or
`reelyard-pi5.img.xz` from the beta release and follow
[the Pi guide](raspberry-pi.md). Use a network cable for the first boot.

**Linux packages, Windows and macOS**: download the `.deb`, `.rpm` or archive
for your system from the beta release and follow the
[install guide](install.md). The archives include an `INSTALL.txt`.

## What to try

Whatever you'd normally do is the most useful test. If you want a list:

1. **First run**: the setup wizard (folders, suppliers), signing in.
2. **Find and track** a movie and a series with a quality profile; check that
   a release is picked, downloaded and moved into the library with subtitles
   in your languages.
3. **Watch it**: on your TV through DLNA (it shows up as *Reelyard*), or in
   Jellyfin, Emby or Plex. Tell us your TV or player's brand and model,
   whether it worked or not, and whether subtitles showed.
4. **Viewers**: in **Settings → Viewers**, add a family member, limit them to
   a few titles and show their server only on one TV. Check that TV lists
   "Reelyard · *name*" with just those titles, and that resume points there
   are kept apart from yours.
5. **Leave it running** for a few days: new episodes, restarts, a full disk,
   a VPN going down (if you use `REELYARD_BIND_INTERFACE`).
6. **Update** to the next beta: pull `:beta` again, or install the new
   package, and check your settings and library survive.

## Reporting

Open an [issue](https://github.com/reelyard/server/issues/new/choose):
**Something's wrong** for bugs, **Idea or feedback** for the rest. The
version is at `http://<your machine>:8787/api/v1/health`.

Issues are public, so don't post passwords, API keys, addon URLs that
contain keys, or your list of titles; trim log lines before pasting them.
