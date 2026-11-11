#!/bin/sh
# Runs after the .deb/.rpm removes Harbor. Your data in /var/lib/harbor
# (database, downloads, library) is kept; delete it by hand if you want.
set -e
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
