#!/bin/sh
# install.sh — download latest gonnxd release and set up the system.
#
# Usage (root or sudo):
#   curl -fsSL https://raw.githubusercontent.com/nikita-popov/gonnx/master/install.sh | sh
#
# What this script does:
#   1. Downloads the latest gonnxd binary for linux/amd64 or linux/arm64
#   2. Creates a dedicated system user 'gonnx' (no login shell)
#   3. Creates directories: /var/lib/gonnx  /etc/gonnx
#   4. Writes a default env config to /etc/gonnx/gonnxd.env  (if missing)
#   5. Installs a systemd unit and enables+starts the service
#
# To install for the current user only (no systemd), run:
#   GONNX_USER_INSTALL=1 curl -fsSL ... | sh

set -e

REPO="nikita-popov/gonnx"
BIN="gonnxd"
SVC="gonnxd"
RUN_USER="gonnx"
DATA_DIR="/var/lib/gonnx"
CONF_DIR="/etc/gonnx"
ENV_FILE="${CONF_DIR}/gonnxd.env"
SYSTEMD_DIR="/etc/systemd/system"
UNIT_FILE="${SYSTEMD_DIR}/${SVC}.service"

# ── helpers ──────────────────────────────────────────────────────────────────

die() { echo "error: $*" >&2; exit 1; }

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$1"
  else
    die "curl or wget is required"
  fi
}

fetch_file() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  else
    wget -qO "$2" "$1"
  fi
}

# ── detect OS / arch ─────────────────────────────────────────────────────────

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
[ "$OS" = "linux" ] || die "unsupported OS: $OS"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

# ── resolve latest release ───────────────────────────────────────────────────

API="https://api.github.com/repos/${REPO}/releases/latest"
RELEASE_JSON="$(fetch "$API")"
VERSION="$(printf '%s' "$RELEASE_JSON" | grep '"tag_name"' | head -1 \
  | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
[ -n "$VERSION" ] || die "could not determine latest release"

ASSET="${BIN}-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"

# ── user-only install (no root) ───────────────────────────────────────────────

if [ "${GONNX_USER_INSTALL:-0}" = "1" ] || [ "$(id -u)" -ne 0 ]; then
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"
  DEST="${INSTALL_DIR}/${BIN}"
  echo "[user install] ${BIN} ${VERSION} (${OS}/${ARCH}) -> ${DEST}"
  TMPFILE="$(mktemp)"
  trap 'rm -f "$TMPFILE"' EXIT
  fetch_file "$URL" "$TMPFILE"
  chmod +x "$TMPFILE"
  mv "$TMPFILE" "$DEST"
  echo "done: $DEST"
  case ":${PATH}:" in
    *:"${INSTALL_DIR}":*) ;;
    *)
      echo "note: add to your shell profile:"
      echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
      ;;
  esac
  exit 0
fi

# ── system install (root) ─────────────────────────────────────────────────────

echo "==> installing ${BIN} ${VERSION} (${OS}/${ARCH})"

# 1. binary
DEST="/usr/local/bin/${BIN}"
TMPFILE="$(mktemp)"
trap 'rm -f "$TMPFILE"' EXIT
fetch_file "$URL" "$TMPFILE"
chmod +x "$TMPFILE"
mv "$TMPFILE" "$DEST"
echo "    binary   -> $DEST"

# 2. dedicated user
if ! id "$RUN_USER" >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /sbin/nologin \
    --comment "gonnx daemon" "$RUN_USER"
  echo "    user     -> $RUN_USER (system)"
fi

# 3. directories
mkdir -p "$DATA_DIR" "$CONF_DIR"
chown "${RUN_USER}:${RUN_USER}" "$DATA_DIR"
chmod 750 "$DATA_DIR"
echo "    data     -> $DATA_DIR"
echo "    config   -> $CONF_DIR"

# 4. default env config (never overwrite existing)
if [ ! -f "$ENV_FILE" ]; then
  cat > "$ENV_FILE" <<'EOF'
# gonnxd environment configuration
# Uncomment and adjust as needed.

# TCP address to listen on (default: 127.0.0.1:11434)
#GONNXD_ADDR=127.0.0.1:11434

# Directory where model bundles are stored
#GONNXD_MODELS_DIR=/var/lib/gonnx/models

# Log level: debug | info | warn | error  (default: info)
#GONNXD_LOG_LEVEL=info

# Execution provider: cpu | cuda | dml  (default: cpu)
#GONNXD_PROVIDER=cpu

# Max concurrent worker processes (default: 1)
#GONNXD_MAX_WORKERS=1
EOF
  chmod 640 "$ENV_FILE"
  chown "root:${RUN_USER}" "$ENV_FILE"
  echo "    env      -> $ENV_FILE"
else
  echo "    env      -> $ENV_FILE (already exists, skipped)"
fi

# 5. systemd unit
if [ -d "$SYSTEMD_DIR" ]; then
  cat > "$UNIT_FILE" <<EOF
[Unit]
Description=gonnx ONNX inference daemon
Documentation=https://github.com/${REPO}
After=network.target

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_USER}
EnvironmentFile=-${ENV_FILE}
ExecStart=/usr/local/bin/${BIN} serve
Restart=on-failure
RestartSec=5s

# hardening
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=strict
ReadWritePaths=${DATA_DIR}
ProtectHome=true
CapabilityBoundingSet=
AmbientCapabilities=
LockPersonality=true
RestrictNamespaces=true
RestrictRealtime=true
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM

[Install]
WantedBy=multi-user.target
EOF
  echo "    unit     -> $UNIT_FILE"

  systemctl daemon-reload

  if systemctl is-active --quiet "$SVC"; then
    systemctl restart "$SVC"
    echo "    service  -> restarted"
  else
    systemctl enable --now "$SVC"
    echo "    service  -> enabled and started"
  fi
else
  echo "    systemd not found — skipping service setup"
  echo "    run manually: ${DEST} serve"
fi

echo ""
echo "done. gonnxd ${VERSION} is installed."
echo "  status : systemctl status ${SVC}"
echo "  logs   : journalctl -u ${SVC} -f"
echo "  config : ${ENV_FILE}"
echo "  data   : ${DATA_DIR}"
