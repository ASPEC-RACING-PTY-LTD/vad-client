#!/usr/bin/env bash
# VaultDock server installer redirect (transitional, C21).
#
# The server moved to https://github.com/ASPEC-RACING-PTY-LTD/vad-server.
# This stub is kept for two release cycles so existing installs and old
# one-line commands keep working. It downloads the current installer from
# vad-server and runs it with the same arguments.
set -euo pipefail

NEW_REPO="ASPEC-RACING-PTY-LTD/vad-server"
URL="https://raw.githubusercontent.com/${NEW_REPO}/main/install.sh"

echo "VaultDock server now lives in ${NEW_REPO}; fetching its installer..." >&2

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
if command -v curl >/dev/null 2>&1; then
	curl -fsSL "$URL" -o "$tmp"
elif command -v wget >/dev/null 2>&1; then
	wget -qO "$tmp" "$URL"
else
	echo "Neither curl nor wget is available." >&2
	exit 1
fi

export VAULTDOCK_REPO="${VAULTDOCK_REPO:-$NEW_REPO}"
bash "$tmp" "$@"
