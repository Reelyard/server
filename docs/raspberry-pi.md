# Reelyard on a Raspberry Pi

## Ready-made image (Pi 4 and Pi 5)

Each release has `reelyard-pi4.img.xz` and `reelyard-pi5.img.xz`: Debian
Trixie (64-bit) with Reelyard and ffmpeg preinstalled and its service enabled.

1. Download the image for your board.
2. In **Raspberry Pi Imager**, choose your device, then **Choose OS → Use
   custom** and pick the `.img.xz`, then your SD card or USB SSD.
3. Connect the Pi to your network with a cable and power it on.
4. After a minute or two, open <http://reelyard.local:8787> (or
   `http://<the Pi's IP>:8787`). The first visit creates your account.

Notes:

- **Use a USB drive for media.** An SD card is too small and too slow for
  downloads. Plug in a drive, then choose its folders in the setup wizard
  (for example `/media/usb/downloads` and `/media/usb/library`, on the same
  drive so imports are hardlinks).
- **Wi-Fi, user and SSH from Imager's settings**: the image includes cloud-init
  so Imager's customisation can apply, but this hasn't been confirmed on
  hardware yet. A network cable always works.
- The Pi shows up on your TVs as **Reelyard** (DLNA) and at `reelyard.local`.
- A Pi 4 or 5 with 2 GB or more is plenty. Pi 3 and Zero 2 W aren't built
  for.

## On an existing Raspberry Pi OS (64-bit)

Install the arm64 `.deb` from the release:

```sh
sudo apt install ./reelyard_*_arm64.deb ffmpeg
```

The package creates a `reelyard` user and starts the `reelyard` service, as on
any Debian system.

## How the image is built

[`deploy/rpi`](../deploy/rpi) holds the [rpi-image-gen](https://github.com/raspberrypi/rpi-image-gen)
configs (`config/reelyard-pi4.yaml`, `config/reelyard-pi5.yaml`) and the
`reelyard` layer (ffmpeg, avahi for `reelyard.local`, cloud-init, service
enabled). The [Raspberry Pi image workflow](../.github/workflows/rpi-image.yml)
builds both on an arm64 GitHub runner after each release and attaches them to
it.
