#!/bin/sh
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
ENV_FILE="$SCRIPT_DIR/.env"
ENV_EXAMPLE="$SCRIPT_DIR/.env.example"

generate_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | cut -c1-32
    return
  fi
  if [ -r /dev/urandom ]; then
    tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32
    return
  fi
  date +%s%N | sha256sum | cut -c1-32
}

set_env_value() {
  key="$1"
  value="$2"
  tmp_file="$ENV_FILE.tmp"
  found=0

  : >"$tmp_file"
  if [ -f "$ENV_FILE" ]; then
    while IFS= read -r line || [ -n "$line" ]; do
      case "$line" in
        "$key="*|"# $key="*|"#$key="*)
          printf '%s=%s\n' "$key" "$value" >>"$tmp_file"
          found=1
          ;;
        *)
          printf '%s\n' "$line" >>"$tmp_file"
          ;;
      esac
    done <"$ENV_FILE"
  fi

  if [ "$found" -eq 0 ]; then
    printf '%s=%s\n' "$key" "$value" >>"$tmp_file"
  fi
  mv "$tmp_file" "$ENV_FILE"
}

if [ ! -f "$ENV_FILE" ]; then
  if [ -f "$ENV_EXAMPLE" ]; then
    cp "$ENV_EXAMPLE" "$ENV_FILE"
  else
    cat >"$ENV_FILE" <<'EOF'
POSTGRES_DB=mail_admin
POSTGRES_USER=mail_admin
POSTGRES_PASSWORD=CHANGE_ME_STRONG_PASSWORD
TZ=Asia/Shanghai
EOF
  fi
fi

POSTGRES_PASSWORD_VALUE="$(grep -E '^POSTGRES_PASSWORD=' "$ENV_FILE" 2>/dev/null | tail -n 1 | cut -d '=' -f2- || true)"

if [ -z "$POSTGRES_PASSWORD_VALUE" ] || [ "$POSTGRES_PASSWORD_VALUE" = "CHANGE_ME_STRONG_PASSWORD" ] || [ "$POSTGRES_PASSWORD_VALUE" = "postgres" ]; then
  POSTGRES_PASSWORD_VALUE="$(generate_password)"
  set_env_value "POSTGRES_PASSWORD" "$POSTGRES_PASSWORD_VALUE"
  echo "已自动生成 PostgreSQL 强密码并写入 .env"
fi

cd "$SCRIPT_DIR"
if [ "${1:-}" = "--build" ]; then
  docker compose up -d --build
else
  docker compose up -d
fi
