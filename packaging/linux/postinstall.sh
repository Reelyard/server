#!/bin/sh
# Runs after the .deb/.rpm installs or upgrades Reelyard.
set -e

if ! getent group reelyard >/dev/null 2>&1; then
	groupadd --system reelyard
fi
if ! getent passwd reelyard >/dev/null 2>&1; then
	nologin=/bin/false
	for s in /usr/sbin/nologin /sbin/nologin; do
		[ -x "$s" ] && nologin=$s && break
	done
	useradd --system --gid reelyard --home-dir /var/lib/reelyard --no-create-home \
		--shell "$nologin" --comment "Reelyard media tracker" reelyard
fi
install -d -o reelyard -g reelyard -m 0775 /var/lib/reelyard

if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
	if systemctl is-enabled --quiet reelyard 2>/dev/null; then
		# Upgrade: pick up the new binary.
		systemctl try-restart reelyard >/dev/null 2>&1 || true
	else
		systemctl enable --now reelyard >/dev/null 2>&1 || true
		echo "Reelyard is running. Open http://localhost:8787 to finish setting it up."
	fi
else
	echo "Start Reelyard with: sudo -u reelyard reelyard serve --system"
fi
exit 0
