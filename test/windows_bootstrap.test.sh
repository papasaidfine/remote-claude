#!/usr/bin/env bash
# Runs the PowerShell test suite; skips (exit 0) when pwsh is unavailable.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PWSH="$(command -v pwsh || true)"
[[ -z "$PWSH" && -x "$HOME/.local/opt/pwsh/pwsh" ]] && PWSH="$HOME/.local/opt/pwsh/pwsh"
if [[ -z "$PWSH" ]]; then
  echo "skip - pwsh not available"
  exit 0
fi
exec "$PWSH" -NoProfile -File "$HERE/windows_bootstrap.test.ps1"
