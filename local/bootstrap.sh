#!/usr/bin/env bash
#
# bootstrap.sh — platform dispatcher for the reverse SSH bootstrap tool.
# Detects the local OS and runs the matching bootstrap script.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

case "$(uname -s)" in
  Darwin)
    exec "$HERE/bootstrap-macos.sh" "$@"
    ;;
  Linux)
    exec "$HERE/bootstrap-linux.sh" "$@"
    ;;
  MINGW*|MSYS*|CYGWIN*)
    echo "Windows detected: run bootstrap-windows.ps1 from an elevated (Administrator) PowerShell:" >&2
    echo "    Set-ExecutionPolicy -Scope Process Bypass -Force" >&2
    echo "    .\\bootstrap-windows.ps1" >&2
    exit 1
    ;;
  *)
    echo "Unsupported platform: $(uname -s)" >&2
    exit 1
    ;;
esac
