#!/usr/bin/env bash
#
# install.sh — download the prebuilt remote-claude binary for this macOS/Linux
# machine and launch it. Paste this one-liner:
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.sh)
#
set -euo pipefail

REPO="papasaidfine/remote-claude"

case "$(uname -s)" in
  Linux)  goos=linux ;;
  Darwin) goos=darwin ;;
  *) echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

asset="remote-claude_${goos}_${goarch}"
url="https://github.com/${REPO}/releases/latest/download/${asset}"
dest_dir="$HOME/.local/bin"
dest="$dest_dir/remote-claude"

mkdir -p "$dest_dir"
echo "Downloading ${asset} ..."
curl -fsSL "$url" -o "$dest"
chmod +x "$dest"
echo "Installed to $dest"

# Re-running the installer updates in place; launch the menu.
exec "$dest"
