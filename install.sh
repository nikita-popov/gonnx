#!/bin/sh
set -e

REPO="nikita-popov/gonnx"
BIN="gonnxd"

# --- detect arch ---
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *)
    echo "unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
if [ "$OS" != "linux" ]; then
  echo "unsupported OS: $OS (only linux is supported)" >&2
  exit 1
fi

ASSET="${BIN}-${OS}-${ARCH}"

# --- resolve latest release ---
API="https://api.github.com/repos/${REPO}/releases/latest"
if command -v curl >/dev/null 2>&1; then
  RELEASE_JSON="$(curl -fsSL "$API")"
elif command -v wget >/dev/null 2>&1; then
  RELEASE_JSON="$(wget -qO- "$API")"
else
  echo "curl or wget is required" >&2
  exit 1
fi

VERSION="$(printf '%s' "$RELEASE_JSON" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
if [ -z "$VERSION" ]; then
  echo "could not determine latest release" >&2
  exit 1
fi

URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"

# --- choose install dir ---
if [ "$(id -u)" -eq 0 ]; then
  INSTALL_DIR="/usr/local/bin"
elif [ -d "$HOME/.local/bin" ]; then
  INSTALL_DIR="$HOME/.local/bin"
else
  mkdir -p "$HOME/.local/bin"
  INSTALL_DIR="$HOME/.local/bin"
fi

DEST="${INSTALL_DIR}/${BIN}"

echo "installing ${BIN} ${VERSION} (${OS}/${ARCH}) -> ${DEST}"

TMPFILE="$(mktemp)"
trap 'rm -f "$TMPFILE"' EXIT

if command -v curl >/dev/null 2>&1; then
  curl -fsSL -o "$TMPFILE" "$URL"
else
  wget -qO "$TMPFILE" "$URL"
fi

chmod +x "$TMPFILE"
mv "$TMPFILE" "$DEST"

echo "done: $DEST"

# warn if install dir is not in PATH
case ":${PATH}:" in
  *:"${INSTALL_DIR}":*) ;;
  *)
    echo "note: ${INSTALL_DIR} is not in your PATH"
    echo "      add the following to your shell profile:"
    echo "      export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac
