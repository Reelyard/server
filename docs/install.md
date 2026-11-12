# Installing Reelyard

Downloads are on the [releases page](https://github.com/reelyard/server/releases).
After installing, open `http://<that machine>:8787` (or <http://localhost:8787>
on the same machine): the first visit creates your account and walks you
through folders and suppliers.

Only download content you have the rights to.

## Which install is for me?

| You have… | Use | Guide |
|---|---|---|
| A NAS: ZimaOS/CasaOS, Unraid, TrueNAS SCALE, Synology, QNAP, OpenMediaVault | The Docker image | [NAS guide](nas.md) |
| A spare Raspberry Pi 4 or 5 | The ready-made Pi image | [Raspberry Pi guide](raspberry-pi.md) |
| A Linux server or PC with Docker | The Docker image | [Docker](#docker) |
| Debian, Ubuntu, Raspberry Pi OS | The `.deb` package | [Linux packages](#linux-deb-and-rpm) |
| Fedora, RHEL, openSUSE | The `.rpm` package | [Linux packages](#linux-deb-and-rpm) |
| Another Linux | The `linux` archive | [Linux archive](#linux-archive) |
| Windows 10 / 11 | The `windows` zip | [Windows](#windows) |
| A Mac | The `darwin` archive | [macOS](#macos) |

Builds are for amd64 (Intel/AMD) and arm64 (Apple Silicon, Raspberry Pi,
ARM servers). **ffmpeg** is optional but recommended: Reelyard uses it to
check audio languages, put your preferred audio track first, make thumbnails
and convert video for TVs. The Docker
image and the Pi image include it.

## Docker

```sh
docker run -d --name reelyard --network host --restart unless-stopped \
  -e PUID=1000 -e PGID=1000 -e TZ=Etc/UTC \
  -v reelyard-config:/config -v /srv/media:/data \
  ghcr.io/reelyard/server:latest
```

Or with Compose: [`deploy/compose.yaml`](../deploy/compose.yaml) (edit the
paths, PUID/PGID and TZ, then `docker compose up -d`).

- `/config` holds the database, settings and logs; `/data` holds `downloads/`
  and `library/`. Keeping both on that one volume lets imports use hardlinks
  instead of copies.
- `--network host` lets TVs find the DLNA server. Without DLNA you can publish
  ports instead: `-p 8787:8787 -p 42069:42069 -p 42069:42069/udp`.
- `PUID`/`PGID` set the user that owns your files (`id <user>` shows yours);
  `UMASK` sets their permissions (default `002`).

**Update:** `docker pull ghcr.io/reelyard/server:latest`, then recreate the
container (`docker compose up -d`, or `docker rm -f reelyard` and the same
`docker run`). Settings live in `/config` and survive.

**Uninstall:** `docker rm -f reelyard`, then `docker volume rm reelyard-config`
(or delete your config folder) to remove settings and the database. Your media
folder is left alone.

## Linux (.deb and .rpm)

```sh
sudo apt install ./reelyard_*_amd64.deb        # Debian, Ubuntu, Raspberry Pi OS (arm64: _arm64.deb)
sudo dnf install ./reelyard-*.x86_64.rpm       # Fedora, RHEL (arm64: .aarch64.rpm)
sudo zypper install ./reelyard-*.x86_64.rpm    # openSUSE
sudo apt install ffmpeg                        # or dnf / zypper
```

The package creates a `reelyard` system user, keeps everything in
`/var/lib/reelyard` and starts the `reelyard` service
(`systemctl status reelyard`, logs: `journalctl -u reelyard`).

To keep media on another disk, choose the folders in the setup wizard (or
**Settings**), then let the service write there:
`sudo systemctl edit reelyard` and add

```ini
[Service]
ReadWritePaths=/srv/media
```

and make the folder writable by the `reelyard` user
(`sudo chown -R reelyard:reelyard /srv/media`, or add `reelyard` to a group
that owns it).

**Update:** install the newer package with the same command; the service
restarts on the new version.

**Uninstall:** `sudo apt remove reelyard` (or `dnf remove` / `zypper remove`).
Your data in `/var/lib/reelyard` is kept, even with `apt purge`. To remove it
too: `sudo rm -rf /var/lib/reelyard && sudo userdel reelyard`.

## Linux archive

```sh
tar xzf reelyard_*_linux_amd64.tar.gz
./reelyard serve                       # try it in the foreground
```

To run it as a service:

```sh
sudo install -m 0755 reelyard /usr/local/bin/reelyard
sudo reelyard install && sudo reelyard start               # system service, data in /var/lib/reelyard
# or, without root:
reelyard install --user && reelyard start --user           # data in ~/.local/share/reelyard
```

**Update:** `sudo reelyard stop` (or `reelyard stop --user`), replace
`/usr/local/bin/reelyard` with the new binary, then `start` again.

**Uninstall:** `sudo reelyard uninstall` (or `reelyard uninstall --user`),
delete `/usr/local/bin/reelyard`, and delete the data folder if you no longer
want it.

## Windows

Unzip `reelyard_*_windows_amd64.zip` (or `_arm64`).

- **Try it:** double-click `reelyard.exe` and allow it on **private networks**
  when Windows Firewall asks.
- **Run it as a service** (starts with Windows): copy `reelyard.exe` to
  `C:\Program Files\Reelyard\`, open Terminal **as Administrator** and run

  ```powershell
  cd "C:\Program Files\Reelyard"
  .\reelyard.exe install
  .\reelyard.exe start
  ```

  A service can't show the firewall prompt: add the rules listed in
  `INSTALL.txt` inside the zip.

Data, downloads and the library are in `C:\ProgramData\Reelyard`.

**ffmpeg:** Reelyard looks for `ffmpeg.exe` and `ffprobe.exe` on the PATH.
`winget install Gyan.FFmpeg` is enough when you start Reelyard by
double-clicking. The service only sees the *system* PATH: unzip an ffmpeg
build to, say, `C:\Program Files\ffmpeg`, add its `bin` folder under
**System Properties → Environment Variables → System variables → Path**, then
`.\reelyard.exe restart`.

**Update:** `.\reelyard.exe stop`, replace `reelyard.exe` with the new one,
`.\reelyard.exe start`.

**Uninstall:** `.\reelyard.exe uninstall`, delete
`C:\Program Files\Reelyard`, and delete `C:\ProgramData\Reelyard` to remove
your data. Remove the firewall rules with
`netsh advfirewall firewall delete rule name="Reelyard web UI"` (and the same
for each rule name you added).

## macOS

```sh
tar xzf reelyard_*_darwin_arm64.tar.gz     # Intel Macs: _darwin_amd64
xattr -d com.apple.quarantine reelyard     # once: the binary isn't notarized
sudo install -m 0755 reelyard /usr/local/bin/reelyard
reelyard install --user && reelyard start --user    # starts now and at every login
brew install ffmpeg                         # optional, recommended
```

Data is in `~/Library/Application Support/Reelyard`.

A Homebrew cask (`brew install --cask reelyard/tap/reelyard`) is planned but
not published yet.

**Update:** `reelyard stop --user`, replace `/usr/local/bin/reelyard` (and run
the `xattr` line on the new file), then `reelyard start --user`.

**Uninstall:** `reelyard uninstall --user`, delete
`/usr/local/bin/reelyard`, and delete
`~/Library/Application Support/Reelyard` to remove your data.

## Raspberry Pi image

See the [Raspberry Pi guide](raspberry-pi.md). The image runs the same
`reelyard` service as the `.deb`, so you **update** it the same way: copy the
newer `reelyard_*_arm64.deb` to the Pi and run
`sudo apt install ./reelyard_*_arm64.deb` from a terminal on it.

## From source

Needs Go 1.26+ and Node.js 24+.

```sh
make web && make build
bin/reelyard serve
```

Without `make` (e.g. on Windows): `cd web && npm ci && npm run build && cd ..`,
then `go build -o reelyard.exe ./cmd/reelyard` and `reelyard.exe serve`.

## Good to know

**Where your data lives** (database `reelyard.db`, `config.yaml`, logs, and by
default `downloads/` and `library/`):

| Install | Folder |
|---|---|
| Docker / NAS | Whatever you mounted at `/config` (media under `/data`) |
| `.deb` / `.rpm`, Pi image, `sudo reelyard install` | `/var/lib/reelyard` |
| Linux `--user` service or `reelyard serve` | `~/.local/share/reelyard` |
| macOS | `~/Library/Application Support/Reelyard` |
| Windows | `C:\ProgramData\Reelyard` |

**Back up** by copying that folder while Reelyard is stopped. Updates migrate
the database automatically on first start, and older versions aren't made to
read a newer database, so copy the folder before updating if you might want
to go back.

**Ports** (open them in your firewall if you use one):

| Port | For |
|---|---|
| 8787/tcp | Web UI and API |
| 42069/tcp+udp | Torrents (peers connect here) |
| 8200/tcp, 1900/udp | DLNA (TVs on your network) |
| 8201/tcp and up | One per [viewer](../README.md#viewers-a-server-per-family-member) with their own DLNA server |

**Commands** (any install except Docker): `reelyard status`, `stop`,
`start`, `restart`, `uninstall`, `reset-password`, `version`. Add `--user` for
a per-user service; on Windows use `reelyard.exe` from an Administrator
terminal.

Upgrading from Harbor (the old name)? See the
[README](../README.md#upgrading-from-harbor).
