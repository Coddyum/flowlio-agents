#!/usr/bin/env bash
#
# demo-wake.sh — watch the cross-repo wake loop close, on this machine, with real processes.
#
# It brings up a throwaway self-host stack (engine + waker) against the dev Postgres, connects two
# fake repos WEB and CORE, gives CORE a STUB agent (a script that just logs it was launched), then
# has WEB ask CORE a question. The engine pushes POST /wake to CORE's waker on 127.0.0.1, the waker
# launches the stub in CORE's directory — and the log line proves the loop closed with no human.
#
# Everything lives in a temp directory with its own XDG_CONFIG_HOME: it touches neither your real
# credentials nor your real repos, and cleans up on exit. Requires: the dev DB up (make dev-up) and
# Go on PATH. This is a hand-run demo, not a test — the proof under CI is the integration test
# TestWakeLoopClosesWithoutAHuman.
set -euo pipefail

DB_URL="${FLOWLIO_TEST_DATABASE_URL:-postgres://flowlio:flowlio@localhost:5433/flowlio?sslmode=disable}"
PORT="${DEMO_PORT:-8099}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

WORK="$(mktemp -d)"
# Unique project slug per run: the slug is global on the instance, so a second demo would collide.
TEAM="demo-$RANDOM$RANDOM"
export XDG_CONFIG_HOME="$WORK/config"
BIN="$WORK/bin"
MARKER="$WORK/core-was-woken.log"
ENGINE_PID=""
WAKER_PID=""

cleanup() {
	# Silence job-control "Terminated" notices by reaping the jobs we kill.
	if [ -n "$WAKER_PID" ]; then kill "$WAKER_PID" 2>/dev/null || true; wait "$WAKER_PID" 2>/dev/null || true; fi
	if [ -n "$ENGINE_PID" ]; then kill "$ENGINE_PID" 2>/dev/null || true; wait "$ENGINE_PID" 2>/dev/null || true; fi
	rm -rf "$WORK"
}
trap cleanup EXIT

say() { printf '\n\033[1;36m▶ %s\033[0m\n' "$*"; }

say "Building flowlio and flowlio-api"
go build -o "$BIN/flowlio" "$ROOT/cmd/flowlio"
go build -o "$BIN/flowlio-api" "$ROOT/cmd/api"
export PATH="$BIN:$PATH"

say "Minting a throwaway admin token (fresh XDG, so this never touches your real creds)"
DATABASE_URL="$DB_URL" ADDR=":$PORT" ENV=dev flowlio-api rotate-admin >/dev/null

say "Starting the engine on 127.0.0.1:$PORT"
DATABASE_URL="$DB_URL" ADDR=":$PORT" ENV=dev flowlio-api >"$WORK/engine.log" 2>&1 &
ENGINE_PID=$!
for _ in $(seq 1 40); do
	flowlio whoami >/dev/null 2>&1 && break
	sleep 0.25
done
flowlio whoami >/dev/null 2>&1 || { echo "engine never came up:"; cat "$WORK/engine.log"; exit 1; }

say "Creating the project 'demo' and its two repos WEB and CORE (they trust each other on creation)"
flowlio team create $TEAM "Demo project" >/dev/null
flowlio project create WEB "Web repo" --team $TEAM >/dev/null
flowlio project create CORE "Core repo" --team $TEAM >/dev/null

WEBDIR="$WORK/web"
COREDIR="$WORK/core"
mkdir -p "$WEBDIR" "$COREDIR"

say "Connecting each repo from its own directory (captures its path for the waker)"
(cd "$WEBDIR" && flowlio connect WEB --project $TEAM --yes >/dev/null)
(cd "$COREDIR" && flowlio connect CORE --project $TEAM --yes >/dev/null)

say "Giving CORE a stub agent — a script that only logs it was launched"
STUB="$WORK/stub-agent.sh"
cat >"$STUB" <<EOF
#!/bin/sh
echo "CORE's agent was launched by the waker — prompt: \$*" >> "$MARKER"
EOF
chmod +x "$STUB"
(cd "$COREDIR" && flowlio agent set-custom "$STUB {prompt}" >/dev/null)

say "Starting the waker (registers a loopback callback per repo with the engine)"
flowlio waker >"$WORK/waker.log" 2>&1 &
WAKER_PID=$!
sleep 1.5 # let it register before the event drops

say "WEB asks CORE a question — the event that should wake CORE"
WEBTOKEN="$(sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$XDG_CONFIG_HOME/flowlio/repos/$TEAM/WEB.json")"
[ -n "$WEBTOKEN" ] || { echo "could not read WEB's token"; cat "$XDG_CONFIG_HOME/flowlio/repos/$TEAM/WEB.json"; exit 1; }
curl -sS -X POST "http://127.0.0.1:$PORT/api/issue/" \
	-H "Authorization: Bearer $WEBTOKEN" \
	-H "Content-Type: application/json" \
	-d '{"to_project":"CORE","title":"why is the build red?","body":"since your last change"}' >/dev/null

say "Waiting for CORE's waker to relaunch its agent…"
for _ in $(seq 1 24); do
	[ -s "$MARKER" ] && break
	sleep 0.25
done

echo
if [ -s "$MARKER" ]; then
	printf '\033[1;32m✅ LOOP CLOSED — no human in the middle:\033[0m\n'
	sed 's/^/    /' "$MARKER"
	echo
	echo "    (in production that stub is your real 'claude -r' / 'codex exec' launch.)"
else
	printf '\033[1;31m❌ CORE was not woken.\033[0m\n'
	echo "--- waker log ---"; tail -n 20 "$WORK/waker.log"
	echo "--- engine log ---"; tail -n 20 "$WORK/engine.log"
	exit 1
fi
