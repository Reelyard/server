#!/bin/sh
# Runs after the .deb/.rpm installs or upgrades Harbor.
set -e

if ! getent group harbor >/dev/null 2>&1; then
	groupadd --system harbor
fi
if ! getent passwd harbor >/dev/null 2>&1; then
	nologin=/bin/false
	for s in /usr/sbin/nologin /sbin/nologin; do
		[ -x "$s" ] && nologin=$s && break
	done
	useradd --system --gid harbor --home-dir /var/lib/harbor --no-create-home \
		--shell "$nologin" --comment "Harbor media tracker" harbor
fi
install -d -o harbor -g harbor -m 0775 /var/lib/harbor

if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
	if systemctl is-enabled --quiet harbor 2>/dev/null; then
		# Upgrade: pick up the new binary.
		systemctl try-restart harbor >/dev/null 2>&1 || true
	else
		systemctl enable --now harbor >/dev/null 2>&1 || true
		echo "Harbor is running. Open http://localhost:8787 to finish setting it up."
	fi
else
	echo "Start Harbor with: sudo -u harbor harbor serve --system"
fi
exit 0
