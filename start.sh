#!/bin/sh
set -eu

export HOST="${HOST:-0.0.0.0}"
export PORT="${PORT:-4400}"
export FRONTEND_HOST="${FRONTEND_HOST:-0.0.0.0}"
export FRONTEND_PORT="${FRONTEND_PORT:-4399}"
export BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:${PORT}}"
RESTORE_RESTART_EXIT_CODE="${RESTORE_RESTART_EXIT_CODE:-42}"

start_backend() {
  /app/mail-backend &
  BACKEND_PID="$!"
}

cleanup_xray() {
  for pid in $(ps 2>/dev/null | awk '/[m]ail-admin-xray/ && /config.json/ {print $1}'); do
    kill "$pid" 2>/dev/null || true
  done
}

start_backend

node /app/frontend-server.js &
FRONTEND_PID="$!"

cleanup() {
  kill "$BACKEND_PID" "$FRONTEND_PID" 2>/dev/null || true
  wait "$BACKEND_PID" "$FRONTEND_PID" 2>/dev/null || true
  cleanup_xray
}

trap cleanup INT TERM EXIT

while true; do
  set +e
  wait -n "$BACKEND_PID" "$FRONTEND_PID"
  EXIT_CODE="$?"
  set -e

  if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
    if [ "$EXIT_CODE" -eq "$RESTORE_RESTART_EXIT_CODE" ]; then
      cleanup_xray
      start_backend
      continue
    fi
    kill "$FRONTEND_PID" 2>/dev/null || true
    wait "$FRONTEND_PID" 2>/dev/null || true
    exit "$EXIT_CODE"
  fi

  if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
    kill "$BACKEND_PID" 2>/dev/null || true
    wait "$BACKEND_PID" 2>/dev/null || true
    exit "$EXIT_CODE"
  fi
done

