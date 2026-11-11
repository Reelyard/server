#!/bin/sh
# Runs before the .deb/.rpm removes Reelyard. On upgrades the service keeps
# running; postinstall restarts it.
set -e

upgrade=0
case "$1" in
	upgrade|1) upgrade=1 ;; # deb: "upgrade"; rpm: number of versions left
esac
if [ "$upgrade" = 0 ] && [ -d /run/systemd/system ]; then
	systemctl disable --now reelyard >/dev/null 2>&1 || true
fi
exit 0
