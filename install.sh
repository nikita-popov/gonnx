#!/bin/sh
# install.sh — download latest gonnx release tarball and set up the system.
#
# Usage (root or sudo):
#   curl -fsSL https://raw.githubusercontent.com/nikita-popov/gonnx/master/install.sh | sh
#
# What this script does:
#   1. Downloads gonnx-linux-<arch>-<version>.tar.gz from GitHub Releases
#   2. Verifies SHA-256 checksum
#   3. Installs gonnxd + gonnxctl to /usr/local/bin
#   4. Installs Python SDK wheel (pip install --break-system-packages or venv)
#   5. Creates system user 'gonnx', directories /var/lib/gonnx /etc/gonnx
#   6. Writes /etc/gonnx/gonnxd.env (if missing)
#   7. Installs + enables systemd unit
#
# User-only install (no root, no systemd):
#   GONNX_USER_INSTALL=1 curl -fsSL ... | sh

set -e

REPO="nikita-popov/gonnx"
SVC="gonnxd"
RUN_USER="gonnx"
DATA_DIR="/var/lib/gonnx"
CONF_DIR="/etc/gonnx"
ENV_FILE="${CONF_DIR}/gonnxd.env"
SYSTEMD_DIR="/etc/systemd/system"
UNIT_FILE="${SYSTEMD_DIR}/${SVC}.service"

# ── helpers ───────────────────────────────────────────────────────────────────

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
  local url="$1" dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$dest" "$url"
  else
    wget -qO "$dest" "$url"
  fi
}

verify_sha256() {
  local file="$1" expected="$2"
  if command -v sha256sum >/dev/null 2>&1; then
    echo "${expected}  ${file}" | sha256sum -c --status \
      || die "SHA-256 mismatch for $file"
  elif command -v shasum >/dev/null 2>&1; then
    echo "${expected}  ${file}" | shasum -a 256 -c --status \
      || die "SHA-256 mismatch for $file"
  else
    echo "warn: no sha256sum/shasum found — skipping checksum verification"
  fi
}

# ── detect OS / arch ──────────────────────────────────────────────────────────

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
[ "$OS" = "linux" ] || die "unsupported OS: $OS"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

# ── resolve latest release ────────────────────────────────────────────────────

API="https://api.github.com/repos/${REPO}/releases/latest"
RELEASE_JSON="$(fetch "$API")"
VERSION="$(printf '%s' "$RELEASE_JSON" | grep '"tag_name"' | head -1 \
  | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
[ -n "$VERSION" ] || die "could not determine latest release version"

TARNAME="gonnx-linux-${ARCH}-${VERSION}.tar.gz"
TARURL="https://github.com/${REPO}/releases/download/${VERSION}/${TARNAME}"
SUM_URL="${TARURL}.sha256"

# ── download & verify ─────────────────────────────────────────────────────────

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "==> downloading ${TARNAME}"
fetch_file "$TARURL" "${WORKDIR}/${TARNAME}"

echo "==> verifying checksum"
CHECKSUM_LINE="$(fetch "$SUM_URL")"
EXPECTED_SUM="$(printf '%s' "$CHECKSUM_LINE" | awk '{print $1}')"
verify_sha256 "${WORKDIR}/${TARNAME}" "$EXPECTED_SUM"

echo "==> extracting"
tar -xzf "${WORKDIR}/${TARNAME}" -C "$WORKDIR"

# ── locate extracted files ────────────────────────────────────────────────────

GONNXD="${WORKDIR}/gonnxd"
GONNXCTL="${WORKDIR}/gonnxctl"
WHL="$(ls "${WORKDIR}"/*.whl 2>/dev/null | head -1 || true)"

[ -f "$GONNXD" ]   || die "gonnxd not found in tarball"
[ -f "$GONNXCTL" ] || die "gonnxctl not found in tarball"

# ── user-only install (no root) ───────────────────────────────────────────────

if [ "${GONNX_USER_INSTALL:-0}" = "1" ] || [ "$(id -u)" -ne 0 ]; then
  INSTALL_DIR="${HOME}/.local/bin"
  mkdir -p "$INSTALL_DIR"

  echo "[user install] ${VERSION} (linux/${ARCH}) -> ${INSTALL_DIR}"
  install -m 755 "$GONNXD"   "${INSTALL_DIR}/gonnxd"
  install -m 755 "$GONNXCTL" "${INSTALL_DIR}/gonnxctl"

  if [ -n "$WHL" ]; then
    echo "==> installing Python SDK wheel"
    if python3 -m pip install -q --user "$WHL" 2>/dev/null; then
      echo "    sdk wheel installed (--user)"
    else
      echo "    warn: pip install failed — install manually: pip install ${WHL}"
    fi
  fi

  echo "done: gonnxd + gonnxctl installed to ${INSTALL_DIR}"
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

echo "==> installing gonnx ${VERSION} (linux/${ARCH})"

# 1. binaries
install -m 755 "$GONNXD"   /usr/local/bin/gonnxd
install -m 755 "$GONNXCTL" /usr/local/bin/gonnxctl
echo "    binaries -> /usr/local/bin/{gonnxd,gonnxctl}"

# 2. Python SDK wheel
if [ -n "$WHL" ]; then
  echo "==> installing Python SDK wheel"
  # try pip with --break-system-packages (Debian/Ubuntu 23.04+)
  # fall back to plain pip, then warn
  if python3 -m pip install -q --break-system-packages "$WHL" 2>/dev/null; then
    echo "    sdk wheel installed (system pip)"
  elif python3 -m pip install -q "$WHL" 2>/dev/null; then
    echo "    sdk wheel installed (pip)"
  else
    echo "    warn: could not install SDK wheel automatically."
    echo "    Install manually: pip install ${WHL}"
  fi
else
  echo "    warn: no .whl found in tarball — SDK not installed"
fi

# 3. dedicated system user (no home directory)
if ! id "$RUN_USER" >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /sbin/nologin \
    --comment "gonnx daemon" "$RUN_USER"
  echo "    user     -> $RUN_USER (system)"
fi

# 4. data and config directories
mkdir -p "$DATA_DIR" "$CONF_DIR"
chown "${RUN_USER}:${RUN_USER}" "$DATA_DIR"
chmod 750 "$DATA_DIR"
echo "    data     -> $DATA_DIR"
echo "    config   -> $CONF_DIR"

# 5. env config (never overwrite on upgrades)
if [ ! -f "$ENV_FILE" ]; then
  cat > "$ENV_FILE" <<EOF
# gonnxd environment configuration
# Sourced by the systemd unit as EnvironmentFile.

# State directory (registry, worker sockets). Must be writable by '${RUN_USER}'.
GONNXD_STATE_DIR=${DATA_DIR}

# TCP listen address
#GONNXD_ADDR=127.0.0.1:7860

# Log level: debug | info | warn | error
#GONNXD_LOG_LEVEL=info

# Execution provider: cpu | cuda | dml
#GONNXD_PROVIDER=cpu

# Max concurrent worker processes
#GONNXD_MAX_WORKERS=1
EOF
  chmod 640 "$ENV_FILE"
  chown "root:${RUN_USER}" "$ENV_FILE"
  echo "    env      -> $ENV_FILE"
else
  echo "    env      -> $ENV_FILE (exists, skipped)"
fi

# 6. systemd unit
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
ExecStart=/usr/local/bin/gonnxd
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
  echo "    run manually: /usr/local/bin/gonnxd"
fi

echo ""
echo "done. gonnx ${VERSION} installed."
echo "  status : systemctl status ${SVC}"
echo "  logs   : journalctl -u ${SVC} -f"
echo "  config : ${ENV_FILE}"
echo "  data   : ${DATA_DIR}"
