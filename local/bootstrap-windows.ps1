<#
.SYNOPSIS
  Reverse SSH dev-environment bootstrap for Windows.

.DESCRIPTION
  Prepares this Windows machine so a remote server's Claude / Codex agent can
  SSH back into it through a reverse tunnel:

    local PC  -- ssh -N remote-claude -->  remote server
    remote server 127.0.0.1:<reverse_port>  -->  local PC 127.0.0.1:22

  Presents a menu of independent items -- each is idempotent, shows whether
  it is already configured, and can be run (or fail, or be re-run) on its own:

    1. Incoming SSH: install OpenSSH Server if missing, start sshd, set it
       to auto-start, and harden %ProgramData%\ssh\sshd_config: pubkey auth
       on, password auth off (optional), and comment out the
       "Match Group administrators" block so admin users also use their own
       %USERPROFILE%\.ssh\authorized_keys. Backs up the config and validates
       with `sshd -t` before restarting. The only item that needs an
       elevated (Administrator) PowerShell.
    2. Ensure the default %USERPROFILE%\.ssh\id_ed25519 exists
       (local -> server hop).
    3. Append the server-side public key to authorized_keys with a
       from="127.0.0.1,::1" restriction (dedup by key blob).
    4. Write a managed "Host remote-claude" block into
       %USERPROFILE%\.ssh\config.
    5. Show the local public key to paste into the server-side setup.

.NOTES
  Only item 1 requires an elevated (Administrator) PowerShell; the other
  items run fine without elevation.

.EXAMPLE
  PS> Set-ExecutionPolicy -Scope Process Bypass -Force
  PS> .\bootstrap-windows.ps1
#>
[CmdletBinding()]
param(
    [string]$ServerHost,
    [string]$ServerUser,
    [int]$ServerPort = 0,
    [int]$ReversePort = 0,
    [string]$ServerPublicKey
)

$ErrorActionPreference = 'Stop'

$TunnelAlias  = 'remote-claude'
$KeyName      = 'id_ed25519'
$SshDir       = Join-Path $env:USERPROFILE '.ssh'
$KeyPath      = Join-Path $SshDir $KeyName
$SshConfig    = Join-Path $SshDir 'config'
$AuthKeys     = Join-Path $SshDir 'authorized_keys'
$SshdConfig   = Join-Path $env:ProgramData 'ssh\sshd_config'
$SshdExe      = Join-Path $env:SystemRoot 'System32\OpenSSH\sshd.exe'
$SshKeygenExe = Join-Path $env:SystemRoot 'System32\OpenSSH\ssh-keygen.exe'
$Ts           = Get-Date -Format 'yyyyMMdd-HHmmss'

$BeginMark = "# >>> $TunnelAlias (managed by reverse-ssh-bootstrap) >>>"
$EndMark   = "# <<< $TunnelAlias <<<"

# xray / VLESS client (items 6 and 7)
$RcConfigDir  = Join-Path $env:LOCALAPPDATA 'remote-claude'
$XrayJson     = Join-Path $RcConfigDir 'xray.json'
$XrayLauncher = Join-Path $RcConfigDir 'xray-proxy.ps1'
$XrayVendorBin = Join-Path (Join-Path $RcConfigDir 'bin') 'xray.exe'

function Write-Info { param([string]$Msg) Write-Host "[+] $Msg" -ForegroundColor Green }
function Write-Warn { param([string]$Msg) Write-Host "[!] $Msg" -ForegroundColor Yellow }
function Write-Err  { param([string]$Msg) Write-Host "[x] $Msg" -ForegroundColor Red }

function Read-Default {
    param([string]$Prompt, [string]$Default = '')
    if ($Default) {
        $reply = Read-Host "$Prompt [$Default]"
        if ([string]::IsNullOrWhiteSpace($reply)) { return $Default }
        return $reply.Trim()
    }
    return (Read-Host $Prompt).Trim()
}

function Read-YesNo {
    param([string]$Prompt, [bool]$DefaultYes = $true)
    $hint = if ($DefaultYes) { 'Y/n' } else { 'y/N' }
    while ($true) {
        $reply = Read-Host "$Prompt [$hint]"
        if ([string]::IsNullOrWhiteSpace($reply)) { return $DefaultYes }
        switch -Regex ($reply.Trim()) {
            '^[Yy]' { return $true }
            '^[Nn]' { return $false }
        }
    }
}

# Strict ACL: only SYSTEM, Administrators and the current user (by SID, so it
# works on non-English Windows). OpenSSH rejects keys/config with loose ACLs.
function Set-StrictAcl {
    param([string]$Path, [switch]$Directory)
    $userSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $perm = if ($Directory) { '(OI)(CI)(F)' } else { '(F)' }
    & icacls $Path /inheritance:r | Out-Null
    & icacls $Path /grant "*S-1-5-18:$perm"     | Out-Null   # SYSTEM
    & icacls $Path /grant "*S-1-5-32-544:$perm" | Out-Null   # Administrators
    & icacls $Path /grant "*${userSid}:$perm"   | Out-Null   # current user
}

function Test-IsAdmin {
    $principal = New-Object System.Security.Principal.WindowsPrincipal(
        [System.Security.Principal.WindowsIdentity]::GetCurrent())
    return $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Initialize-SshDir {
    if (-not (Test-Path $SshDir)) { New-Item -ItemType Directory -Path $SshDir | Out-Null }
    if (-not (Test-Path $AuthKeys)) { New-Item -ItemType File -Path $AuthKeys | Out-Null }
    Set-StrictAcl -Path $SshDir -Directory
    Set-StrictAcl -Path $AuthKeys
}

# Set a global sshd directive (replace the first match only; append to the
# end of the file when not found)
function Set-SshdDirective {
    param([string]$Text, [string]$Name, [string]$Value)
    $pattern = "(?m)^[#\t ]*$Name([ \t][^\r\n]*)?\r?$"
    $line = "$Name $Value"
    $regex = [regex]$pattern
    if ($regex.IsMatch($Text)) {
        return $regex.Replace($Text, $line, 1)
    }
    return $Text.TrimEnd() + "`r`n$line`r`n"
}

# ---------------------------------------------------------------- xray / VLESS
# Parse a vless:// URL into an xray client config TEMPLATE (JSON string).
# The inbound is a dokodemo-door with __DOKO_PORT__/__DEST_HOST__/__DEST_PORT__
# placeholders that xray-proxy.ps1 fills in per connection; __LOG_FILE__ points
# the error log at a per-connection file. The VLESS outbound is fully resolved.
function ConvertTo-VlessJson {
    param([string]$Url)
    if ($Url -notmatch '^vless://') { throw 'Not a vless:// URL' }
    $rest = $Url.Substring(8) -replace '#.*$', ''
    if ($rest -notmatch '^([^@]+)@(.+):(\d+)(\?(.*))?$') {
        throw 'Malformed vless:// URL (need uuid@host:port)'
    }
    $uuid  = $Matches[1]
    $vhost = $Matches[2]
    $vport = [int]$Matches[3]
    $query = if ($Matches[5]) { $Matches[5] } else { '' }

    $p = @{ type = 'tcp'; security = 'none' }
    foreach ($pair in ($query -split '&')) {
        if (-not $pair) { continue }
        $k, $v = $pair -split '=', 2
        if ($null -eq $v) { $v = '' }
        $v = [uri]::UnescapeDataString(($v -replace '\+', ' '))
        switch ($k) {
            'type'        { $p.type = $v }
            'network'     { $p.type = $v }
            'security'    { $p.security = $v }
            'flow'        { $p.flow = $v }
            'sni'         { $p.sni = $v }
            'fp'          { $p.fp = $v }
            'pbk'         { $p.pbk = $v }
            'sid'         { $p.sid = $v }
            'alpn'        { $p.alpn = $v }
            'path'        { $p.path = $v }
            'host'        { $p.hosthdr = $v }
            'serviceName' { $p.servicename = $v }
        }
    }
    if (-not $p.security) { $p.security = 'none' }
    if ('reality', 'tls', 'none' -notcontains $p.security) {
        throw "Unsupported security='$($p.security)' (supported: reality, tls, none)"
    }
    if ('tcp', 'ws', 'grpc' -notcontains $p.type) {
        throw "Unsupported network type='$($p.type)' (supported: tcp, ws, grpc)"
    }

    $user = [ordered]@{ id = $uuid; encryption = 'none' }
    if ($p.flow) { $user.flow = $p.flow }

    $stream = [ordered]@{ network = $p.type; security = $p.security }
    switch ($p.security) {
        'reality' {
            if (-not $p.pbk) { throw 'reality requires pbk (publicKey) in the URL' }
            $fp = if ($p.fp) { $p.fp } else { 'chrome' }
            $stream.realitySettings = [ordered]@{
                serverName = "$($p.sni)"; fingerprint = $fp
                publicKey = $p.pbk; shortId = "$($p.sid)"; spiderX = ''
            }
        }
        'tls' {
            $fp = if ($p.fp) { $p.fp } else { 'chrome' }
            $alpn = if ($p.alpn) { @($p.alpn -split ',') } else { @() }
            $stream.tlsSettings = [ordered]@{
                serverName = "$($p.sni)"; fingerprint = $fp; alpn = $alpn
            }
        }
    }
    switch ($p.type) {
        'ws' {
            $path = if ($p.path) { $p.path } else { '/' }
            $stream.wsSettings = [ordered]@{
                path = $path; headers = [ordered]@{ Host = "$($p.hosthdr)" }
            }
        }
        'grpc' { $stream.grpcSettings = [ordered]@{ serviceName = "$($p.servicename)" } }
        'tcp'  { $stream.tcpSettings = @{} }
    }

    $config = [ordered]@{
        log = [ordered]@{ loglevel = 'warning'; error = '__LOG_FILE__' }
        inbounds = @(
            [ordered]@{
                listen = '127.0.0.1'; port = '__DOKO_PORT__'
                protocol = 'dokodemo-door'
                settings = [ordered]@{
                    address = '__DEST_HOST__'; port = '__DEST_PORT__'; network = 'tcp'
                }
            }
        )
        outbounds = @(
            [ordered]@{
                protocol = 'vless'
                settings = [ordered]@{
                    vnext = @([ordered]@{ address = $vhost; port = $vport; users = @($user) })
                }
                streamSettings = $stream
            }
        )
    }
    return ($config | ConvertTo-Json -Depth 10)
}

# ---------------------------------------------------------------- platform
if (-not $env:RC_SOURCED_FOR_TEST) {
    if ($env:OS -ne 'Windows_NT') {
        Write-Err 'This script is for Windows. On macOS, run ./bootstrap-macos.sh instead.'
        exit 1
    }

    Write-Host @'
==========================================================
 Reverse SSH bootstrap (Windows)
 local PC  ->  remote server  ->  (reverse tunnel)  -> local PC
==========================================================
Pick items from the menu below; each one is independent, idempotent,
and shows whether it is already configured. Files this can modify:
  - OpenSSH Server install / sshd service / sshd_config (item 1, Administrator)
  - %USERPROFILE%\.ssh\{config,authorized_keys,id_ed25519}
All modified system files are backed up first (*.claude-bak-<timestamp>).
'@
}

# ---------------------------------------------------------------- menu items
function Invoke-ItemSshd {   # item 1: OpenSSH Server install + harden (admin)
    if (-not (Test-IsAdmin)) {
        Write-Host ('    Start-Process powershell -Verb RunAs -ArgumentList ''-ExecutionPolicy Bypass -File "{0}"''' -f $PSCommandPath)
        throw 'Administrator privileges are required for this item (OpenSSH Server install / sshd_config / service control). Re-run this script from an elevated PowerShell (e.g. the line above) to use it.'
    }
    $DisablePassword = Read-YesNo 'Disable password login for the local sshd (recommended, public key only)' $true

    Write-Info 'Checking whether OpenSSH Server is installed'
    $cap = Get-WindowsCapability -Online | Where-Object Name -like 'OpenSSH.Server*' | Select-Object -First 1
    if (-not $cap) {
        throw 'OpenSSH.Server capability not found; Windows 10 1809+ / Windows 11 is required.'
    }
    if ($cap.State -ne 'Installed') {
        Write-Info 'Installing OpenSSH Server (this can take a few minutes)...'
        Add-WindowsCapability -Online -Name $cap.Name | Out-Null
        Write-Info 'OpenSSH Server installed'
    } else {
        Write-Info 'OpenSSH Server is already installed'
    }
    $clientCap = Get-WindowsCapability -Online | Where-Object Name -like 'OpenSSH.Client*' | Select-Object -First 1
    if ($clientCap -and $clientCap.State -ne 'Installed') {
        Write-Info 'Installing OpenSSH Client (provides ssh / ssh-keygen)...'
        Add-WindowsCapability -Online -Name $clientCap.Name | Out-Null
    }

    # Start once first so OpenSSH generates the default sshd_config and host
    # keys under %ProgramData%\ssh
    Write-Info 'Starting the sshd service and enabling auto-start'
    Set-Service -Name sshd -StartupType Automatic
    Start-Service -Name sshd

    if (-not (Test-Path $SshdConfig)) {
        throw "$SshdConfig not found (it should be generated on the first sshd start)"
    }
    $backupPath = "$SshdConfig.claude-bak-$Ts"
    Copy-Item $SshdConfig $backupPath
    Write-Info "Backed up sshd_config -> $backupPath"

    $raw = Get-Content -Raw $SshdConfig

    # 1) Comment out the administrators_authorized_keys Match block so users
    #    in the Administrators group also use their own
    #    %USERPROFILE%\.ssh\authorized_keys.
    #    Already-commented lines no longer match the pattern, so re-runs are
    #    idempotent.
    $raw = $raw -replace '(?m)^([ \t]*Match[ \t]+Group[ \t]+administrators[ \t]*)\r?$', '# claude-bootstrap disabled: $1'
    $raw = $raw -replace '(?m)^([ \t]*AuthorizedKeysFile[ \t]+__PROGRAMDATA__[/\\]ssh[/\\]administrators_authorized_keys[ \t]*)\r?$', '# claude-bootstrap disabled: $1'

    # 2) Global directives
    $raw = Set-SshdDirective $raw 'PubkeyAuthentication' 'yes'
    $raw = Set-SshdDirective $raw 'AuthorizedKeysFile' '.ssh/authorized_keys'
    if ($DisablePassword) {
        $raw = Set-SshdDirective $raw 'PasswordAuthentication' 'no'
    }

    [System.IO.File]::WriteAllText($SshdConfig, $raw)

    Write-Info 'Validating the sshd config (sshd -t)'
    # sshd -t reports errors on stderr; with EAP=Stop, 2>&1 wraps stderr into
    # an exception (PS 5.1 NativeCommandError), so relax it during validation
    # to make sure the backup gets restored on failure
    $prevEap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $sshdCheck = & $SshdExe -t 2>&1
    $sshdCheckCode = $LASTEXITCODE
    $ErrorActionPreference = $prevEap
    $sshdCheck | ForEach-Object { Write-Host "    $_" }
    if ($sshdCheckCode -ne 0) {
        Copy-Item $backupPath $SshdConfig -Force
        throw 'sshd config validation failed; the backup was restored'
    }
    Write-Info 'Config validation passed, restarting sshd'
    Restart-Service -Name sshd
}

function Invoke-ItemKey {    # item 2: ensure %USERPROFILE%\.ssh\id_ed25519 exists
    Initialize-SshDir
    # Use the default SSH key; generate it only when it does not exist yet.
    if (Test-Path $KeyPath) {
        # ssh-keygen -y with an empty passphrase succeeds only on unprotected keys
        $prevEap = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        $derivedPub = & $SshKeygenExe -y -P '""' -f $KeyPath 2>$null
        $keyProbeCode = $LASTEXITCODE
        $ErrorActionPreference = $prevEap
        if (-not (Test-Path "$KeyPath.pub")) {
            if ($keyProbeCode -ne 0) {
                throw "$KeyPath exists but $KeyPath.pub is missing and could not be derived (passphrase-protected?); please fix and re-run"
            }
            [System.IO.File]::WriteAllText("$KeyPath.pub", ($derivedPub -join "`n") + "`n")
        }
        Write-Info "Using existing SSH key: $KeyPath"
        if ($keyProbeCode -ne 0) {
            Write-Warn 'This key appears to be passphrase-protected; the tunnel will need an ssh-agent to work'
        }
    } else {
        Write-Info "Generating the default SSH key: $KeyPath"
        & $SshKeygenExe -t ed25519 -f $KeyPath -N '""' | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'ssh-keygen failed' }
    }
    Set-StrictAcl -Path $KeyPath
}

function Invoke-ItemAuthorize {  # item 3: authorize the server's connect-back key
    Initialize-SshDir
    Write-Host 'Server-side public key: the .pub of the key that Claude / Codex on the'
    Write-Host 'server will use to SSH back into this machine (setup-server.sh item 1'
    Write-Host 'prints it, or: cat ~/.ssh/id_ed25519.pub on the server).'
    $pubkey = $ServerPublicKey
    if (-not $pubkey) { $pubkey = Read-Default 'Server-side public key' '' }
    if (-not $pubkey) { throw 'No key pasted; nothing changed' }

    $tmp = [System.IO.Path]::GetTempFileName()
    try {
        [System.IO.File]::WriteAllText($tmp, $pubkey + "`n")
        $prevEap = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        & $SshKeygenExe -lf $tmp *> $null
        $keyCheckCode = $LASTEXITCODE
        $ErrorActionPreference = $prevEap
        if ($keyCheckCode -ne 0) {
            throw 'The pasted content is not a valid SSH public key; please check and re-run'
        }
    } finally {
        Remove-Item $tmp -ErrorAction SilentlyContinue
    }
    $blob = ($pubkey -split '\s+' | Where-Object { $_ -like 'AAAA*' } | Select-Object -First 1)
    if (-not $blob) { throw 'Could not parse the key data from the public key' }
    $existing = Get-Content -Raw $AuthKeys -ErrorAction SilentlyContinue
    if ($existing -and $existing.Contains($blob)) {
        Write-Info 'This public key is already in authorized_keys, skipping'
    } else {
        $entry = "from=`"127.0.0.1,::1`",no-agent-forwarding,no-X11-forwarding $pubkey"
        Add-Content -Path $AuthKeys -Value $entry -Encoding ascii
        Write-Info 'Written to authorized_keys (restricted to loopback logins only)'
    }
}

function Invoke-ItemConfig {     # item 4: Host remote-claude block
    Initialize-SshDir
    $srvHost = $ServerHost
    if (-not $srvHost) { $srvHost = Read-Default 'Remote server hostname / IP' }
    if (-not $srvHost) { throw 'Server hostname must not be empty' }
    $srvUser = $ServerUser
    if (-not $srvUser) { $srvUser = Read-Default 'Remote server SSH user' }
    if (-not $srvUser) { throw 'Server user must not be empty' }
    $srvPort = $ServerPort
    if ($srvPort -le 0) { $srvPort = [int](Read-Default 'Remote server SSH port' '22') }
    $revPort = $ReversePort
    if ($revPort -le 0) { $revPort = [int](Read-Default 'Reverse SSH port on the server (used by Claude/Codex to connect back)' '2222') }

    $configBlock = @"
$BeginMark
Host $TunnelAlias
    HostName $srvHost
    User $srvUser
    Port $srvPort
    IdentityFile ~/.ssh/$KeyName
    IdentitiesOnly yes
    RemoteForward 127.0.0.1:$revPort 127.0.0.1:22
    ExitOnForwardFailure yes
    ServerAliveInterval 30
    ServerAliveCountMax 3
    ForwardAgent no
$EndMark
"@

    if (-not (Test-Path $SshConfig)) { New-Item -ItemType File -Path $SshConfig | Out-Null }
    $configRaw = Get-Content -Raw $SshConfig -ErrorAction SilentlyContinue
    if ($null -eq $configRaw) { $configRaw = '' }

    $writeBlock = $true
    if ($configRaw.Contains($BeginMark)) {
        if (Read-YesNo "~\.ssh\config already contains a $TunnelAlias block, update it" $true) {
            Copy-Item $SshConfig "$SshConfig.claude-bak-$Ts"
            Write-Info "Backed up ssh config -> $SshConfig.claude-bak-$Ts"
            $escBegin = [regex]::Escape($BeginMark)
            $escEnd   = [regex]::Escape($EndMark)
            $configRaw = [regex]::Replace($configRaw, "(?s)$escBegin.*?$escEnd(\r?\n)?", '')
        } else {
            Write-Warn 'Keeping the existing block, skipping the write'
            $writeBlock = $false
        }
    } else {
        if ($configRaw -match "(?m)^\s*Host\s+.*\b$TunnelAlias\b") {
            Write-Warn "~\.ssh\config contains a 'Host $TunnelAlias' block that is not managed by this tool."
            Write-Warn 'ssh uses first-match-wins, so the earlier block would override what this tool writes.'
            if (-not (Read-YesNo 'Write the block anyway (cleaning up the old block manually is recommended)' $false)) {
                throw "Aborted. Please remove the old Host $TunnelAlias block and re-run"
            }
        }
        Copy-Item $SshConfig "$SshConfig.claude-bak-$Ts"
        Write-Info "Backed up ssh config -> $SshConfig.claude-bak-$Ts"
    }

    if ($writeBlock) {
        $configRaw = $configRaw.TrimEnd()
        if ($configRaw) { $configRaw += "`r`n`r`n" }
        $configRaw += $configBlock.Replace("`r`n", "`n").Replace("`n", "`r`n") + "`r`n"
        [System.IO.File]::WriteAllText($SshConfig, $configRaw)
        Write-Info "Wrote Host $TunnelAlias to $SshConfig"
    }
    Set-StrictAcl -Path $SshConfig
}

function Invoke-ItemShowKey {    # item 5: print the local public key
    if (-not (Test-Path "$KeyPath.pub")) {
        if (-not (Test-Path $KeyPath)) {
            if (-not (Read-YesNo 'No local key yet - generate it now' $true)) { throw 'No key to show' }
        }
        Invoke-ItemKey
    }
    Write-Host ''
    Write-Info "Local public key - paste it into server/setup-server.sh (item 2) on the server; that authorizes the tunnel login (ssh -N $TunnelAlias):"
    Write-Host ''
    Get-Content "$KeyPath.pub" | Write-Host
    Write-Host ''
}

# ---------------------------------------------------------------- status checks
function Test-StatusSshd {
    $svc = Get-Service -Name sshd -ErrorAction SilentlyContinue
    if (-not $svc -or $svc.Status -ne 'Running') { return $false }
    $raw = Get-Content -Raw $SshdConfig -ErrorAction SilentlyContinue
    return [bool]($raw -match '(?m)^[ \t]*PubkeyAuthentication[ \t]+yes')
}
function Test-StatusKey { return (Test-Path $KeyPath) }
function Test-StatusAuthorize {
    $raw = Get-Content -Raw $AuthKeys -ErrorAction SilentlyContinue
    return [bool]($raw -and $raw.Contains('from="127.0.0.1,::1"'))
}
function Test-StatusConfig {
    $raw = Get-Content -Raw $SshConfig -ErrorAction SilentlyContinue
    return [bool]($raw -and $raw.Contains($BeginMark))
}

# ---------------------------------------------------------------- menu
function Format-Mark { param([bool]$Ok) if ($Ok) { '[done]' } else { '[ -  ]' } }

function Show-Menu {
    Write-Host ''
    Write-Host '----------------------------------------------------------'
    Write-Host ('  1) {0,-50} {1}' -f 'Incoming SSH - OpenSSH Server + harden  [admin]', (Format-Mark (Test-StatusSshd)))
    Write-Host ('  2) {0,-50} {1}' -f 'Local SSH key (~\.ssh\id_ed25519)', (Format-Mark (Test-StatusKey)))
    Write-Host ('  3) {0,-50} {1}' -f "Authorize the server's connect-back key", (Format-Mark (Test-StatusAuthorize)))
    Write-Host ('  4) {0,-50} {1}' -f 'Tunnel config (Host remote-claude)', (Format-Mark (Test-StatusConfig)))
    Write-Host  '  5) Show local public key (paste into server setup)'
    Write-Host  '  q) Quit'
}

if (-not $env:RC_SOURCED_FOR_TEST) {
    :menu while ($true) {
        Show-Menu
        $choice = (Read-Host 'Select [1-5, q]').Trim()
        if ($choice -match '^[Qq]$') { break menu }
        $fn = switch ($choice) {
            '1' { 'Invoke-ItemSshd' }
            '2' { 'Invoke-ItemKey' }
            '3' { 'Invoke-ItemAuthorize' }
            '4' { 'Invoke-ItemConfig' }
            '5' { 'Invoke-ItemShowKey' }
            default { $null }
        }
        if (-not $fn) { Write-Warn "Unknown selection: $choice"; continue }
        Write-Host ''
        try { & $fn }
        catch {
            Write-Err "Item did not complete: $_"
            Write-Err 'Other items are unaffected.'
        }
    }

    Write-Host ''
    Write-Info "Start the tunnel with: ssh -N $TunnelAlias   (keep it running)"
    Write-Info "Then on the server: ssh my-device 'echo ok' should print ok"
}
