#!/bin/sh
# Smoke test: start a built binary, check the API answers and the UI is
# embedded, then stop it. Same check as the CI workflow's "Smoke test" step.
#   scripts/smoke.sh bin/reelyard-smoke
set -eu

bin=${1:?usage: smoke.sh <binary>}
addr=127.0.0.1:${SMOKE_PORT:-18787}
dir=$(mktemp -d)
pid=
cleanup() {
	[ -n "$pid" ] && kill "$pid" 2>/dev/null && wait "$pid" 2>/dev/null
	rm -rf "$dir"
}
trap cleanup EXIT INT TERM

# Torrent port 0: any free port, so a Reelyard already running here doesn't clash.
REELYARD_DATA_DIR="$dir/data" REELYARD_LISTEN="$addr" REELYARD_TORRENT_PORT=0 "$bin" serve >"$dir/log" 2>&1 &
pid=$!

i=0
until curl -fsS "http://$addr/api/v1/health" >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -ge 30 ] || ! kill -0 "$pid" 2>/dev/null; then
		echo "smoke: server did not answer on $addr" >&2
		cat "$dir/log" >&2
		exit 1
	fi
	sleep 1
done

curl -fsS "http://$addr/" | grep -q '<div id="root">' || {
	echo "smoke: UI not embedded (no <div id=\"root\"> at /)" >&2
	exit 1
}
echo "smoke: ok"
