<#
.SYNOPSIS
  Download the prebuilt remote-claude binary for Windows and launch it.

.DESCRIPTION
  Paste this one-liner in PowerShell:

    irm https://raw.githubusercontent.com/papasaidfine/remote-claude/main/install.ps1 | iex

  It fetches the matching release binary into %LOCALAPPDATA%\remote-claude\bin
  and starts the setup menu. Re-running updates in place. Only the "Incoming
  SSH" item needs an elevated (Administrator) PowerShell.

  If GitHub is blocked, set $env:RC_PROXY to your local proxy first; the binary
  then downloads through it.
#>
$ErrorActionPreference = 'Stop'
$repo = 'papasaidfine/remote-claude'

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$asset = "remote-claude_windows_$arch.exe"
$url = "https://github.com/$repo/releases/latest/download/$asset"

$destDir = Join-Path $env:LOCALAPPDATA 'remote-claude\bin'
New-Item -ItemType Directory -Force -Path $destDir | Out-Null
$dest = Join-Path $destDir 'remote-claude.exe'

# Optional proxy for the download — some regions can't reach GitHub directly.
$iwr = @{ UseBasicParsing = $true; Uri = $url; OutFile = $dest }
if ($env:RC_PROXY) {
    $iwr.Proxy = $env:RC_PROXY
    Write-Host "Using proxy $env:RC_PROXY"
}
Write-Host "Downloading $asset ..."
$prevPp = $ProgressPreference
$ProgressPreference = 'SilentlyContinue'
try {
    Invoke-WebRequest @iwr
} finally { $ProgressPreference = $prevPp }
Unblock-File $dest
Write-Host "Installed to $dest"

& $dest
