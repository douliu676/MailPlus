#!/bin/sh
set -eu

export HOST="${HOST:-0.0.0.0}"
export PORT="${PORT:-4400}"
export FRONTEND_HOST="${FRONTEND_HOST:-0.0.0.0}"
export FRONTEND_PORT="${FRONTEND_PORT:-4399}"
export BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:${PORT}}"
RESTORE_RESTART_EXIT_CODE="${RESTORE_RESTART_EXIT_CODE:-42}"
BACKEND_RESTART_DELAY="${BACKEND_RESTART_DELAY:-2}"
BACKEND_RESTART_ATTEMPTS="${BACKEND_RESTART_ATTEMPTS:-20}"

start_backend() {
  /app/mail-backend &
  BACKEND_PID="$!"
}

wait_for_postgres() {
  if ! command -v pg_isready >/dev/null 2>&1; then
    sleep "$BACKEND_RESTART_DELAY"
    return 0
  fi
  if [ -z "${DATABASE_URL:-}" ]; then
    sleep "$BACKEND_RESTART_DELAY"
    return 0
  fi

  attempt=1
  while [ "$attempt" -le "$BACKEND_RESTART_ATTEMPTS" ]; do
    if pg_isready -d "$DATABASE_URL" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$BACKEND_RESTART_DELAY"
    attempt=$((attempt + 1))
  done

  return 1
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
  if [ -n "${BACKEND_PID:-}" ]; then
    kill "$BACKEND_PID" 2>/dev/null || true
    wait "$BACKEND_PID" 2>/dev/null || true
  fi
  if [ -n "${FRONTEND_PID:-}" ]; then
    kill "$FRONTEND_PID" 2>/dev/null || true
    wait "$FRONTEND_PID" 2>/dev/null || true
  fi
  cleanup_xray
}

trap cleanup INT TERM EXIT

while true; do
  set +e
  wait "$BACKEND_PID"
  EXIT_CODE="$?"
  set -e
  BACKEND_PID=""
  cleanup_xray

  if [ "$EXIT_CODE" -eq "$RESTORE_RESTART_EXIT_CODE" ]; then
    echo "database restore completed, waiting for postgres before backend restart"
    if ! wait_for_postgres; then
      echo "postgres did not become ready after restore restart" >&2
      kill "$FRONTEND_PID" 2>/dev/null || true
      wait "$FRONTEND_PID" 2>/dev/null || true
      exit 1
    fi

    if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
      echo "frontend exited before backend restart" >&2
      exit 1
    fi

    sleep "$BACKEND_RESTART_DELAY"
    start_backend
    echo "backend restarted after database restore"
    continue
  fi

  kill "$FRONTEND_PID" 2>/dev/null || true
  wait "$FRONTEND_PID" 2>/dev/null || true
  exit "$EXIT_CODE"
done

