#!/bin/sh
# Starts Reelyard as the user NAS systems expect (PUID/PGID, like
# LinuxServer.io images): files in the library then belong to that user.
#   PUID / PGID  user and group ids (default 1000:1000)
#   UMASK        permissions for new files (default 002: group-writable)
# A container started with --user/`user:` skips this and runs as given.
set -e

umask "${UMASK:-002}"

if [ "$(id -u)" != 0 ]; then
	exec /usr/bin/reelyard "$@"
fi

PUID=${PUID:-1000}
PGID=${PGID:-1000}
case "$PUID$PGID" in
*[!0-9]*)
	echo "PUID and PGID must be numbers (got PUID=$PUID PGID=$PGID)" >&2
	exit 1
	;;
esac

# The config folder belongs to Reelyard: fix its owner if it changed.
data=${REELYARD_DATA_DIR:-/config}
mkdir -p "$data"
if [ "$(stat -c %u:%g "$data")" != "$PUID:$PGID" ]; then
	chown -R "$PUID:$PGID" "$data"
fi
# Media folders may be shared with other apps: create them if missing,
# but never change the owner of existing ones.
for d in "${REELYARD_DOWNLOAD_DIR:-/data/downloads}" "${REELYARD_LIBRARY_DIR:-/data/library}"; do
	if [ ! -e "$d" ]; then
		mkdir -p "$d" && chown "$PUID:$PGID" "$d"
	fi
done

export HOME="$data"
exec su-exec "$PUID:$PGID" /usr/bin/reelyard "$@"
