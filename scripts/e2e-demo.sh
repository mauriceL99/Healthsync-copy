#!/usr/bin/env bash
# End-to-end demo without an iPhone: local relay + simulated device with
# SYNTHETIC data + fetch + summary + dashboard. Uses a temporary state dir.
set -euo pipefail
cd "$(dirname "$0")/.."
BIN=./bin
WORK=$(mktemp -d)
PORT=${PORT:-18787}
trap 'kill $RELAY_PID 2>/dev/null || true; rm -rf "$WORK"' EXIT

"$BIN/healthbridge-relay" --addr "127.0.0.1:$PORT" --db "$WORK/relay.db" 2>"$WORK/relay.log" &
RELAY_PID=$!
for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break; sleep 0.1; done

HB() { "$BIN/healthbridge" --state-dir "$WORK/state" "$@"; }
HB onboard --relay "http://127.0.0.1:$PORT" --name "Demo Agent" --no-terminal-qr | head -6
HB dev simulate-device --days 60 --seed 7
HB fetch
HB summary --period week
HB dashboard --days 30 --out "$PWD/dashboard.html" --force
echo "Dashboard: $PWD/dashboard.html"
