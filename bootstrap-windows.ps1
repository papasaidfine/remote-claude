<#
.SYNOPSIS
  Reverse SSH dev-environment bootstrap for Windows.

.DESCRIPTION
  Prepares this Windows machine so a remote server's Claude / Codex agent can
  SSH back into it through a reverse tunnel:

    local PC  -- ssh -N remote-claude -->  remote server
    remote server 127.0.0.1:<reverse_port>  -->  local PC 127.0.0.1:22

  What it does (idempotent, safe to re-run):
    1. Installs OpenSSH Server if missing, starts sshd, sets it to auto-start.
    2. Hardens %ProgramData%\ssh\sshd_config: pubkey auth on, password auth
       off (optional), loopback-only listen (optional), and comments out the
       "Match Group administrators" block so admin users also use their own
       %USERPROFILE%\.ssh\authorized_keys. Backs up the config and validates
       with `sshd -t` before restarting.
    3. Creates %USERPROFILE%\.ssh + authorized_keys with strict ACLs.
    4. Appends the server-side public key to authorized_keys with a
       from="127.0.0.1,::1" restriction (dedup by key blob).
    5. Generates %USERPROFILE%\.ssh\claude_tunnel_ed25519 for the
       local -> server hop.
    6. Writes a managed "Host remote-claude" block into
       %USERPROFILE%\.ssh\config.
    7. Optionally registers a Scheduled Task that keeps the tunnel up after
       logon (hidden window, auto-reconnect loop).

.NOTES
  Must be run from an elevated (Administrator) PowerShell.

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
    [string]$LocalUser,
    [string]$ServerPublicKey
)

$ErrorActionPreference = 'Stop'

$TunnelAlias  = 'remote-claude'
$KeyName      = 'claude_tunnel_ed25519'
$SshDir       = Join-Path $env:USERPROFILE '.ssh'
$KeyPath      = Join-Path $SshDir $KeyName
$SshConfig    = Join-Path $SshDir 'config'
$AuthKeys     = Join-Path $SshDir 'authorized_keys'
$SshdConfig   = Join-Path $env:ProgramData 'ssh\sshd_config'
$SshdExe      = Join-Path $env:SystemRoot 'System32\OpenSSH\sshd.exe'
$SshExe       = Join-Path $env:SystemRoot 'System32\OpenSSH\ssh.exe'
$SshKeygenExe = Join-Path $env:SystemRoot 'System32\OpenSSH\ssh-keygen.exe'
$TaskName     = 'ClaudeDevTunnel'
$KeepAlivePs1 = Join-Path $SshDir 'remote-claude-keepalive.ps1'
$Ts           = Get-Date -Format 'yyyyMMdd-HHmmss'

$BeginMark = "# >>> $TunnelAlias (managed by reverse-ssh-bootstrap) >>>"
$EndMark   = "# <<< $TunnelAlias <<<"

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

# ---------------------------------------------------------------- platform / admin
if ($env:OS -ne 'Windows_NT') {
    Write-Err 'This script is for Windows. On macOS, run ./bootstrap-macos.sh instead.'
    exit 1
}
$principal = New-Object System.Security.Principal.WindowsPrincipal(
    [System.Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Err 'Administrator privileges are required (OpenSSH Server install / sshd_config / service control).'
    Write-Err 'Please re-run from an elevated PowerShell, e.g.:'
    Write-Host ('    Start-Process powershell -Verb RunAs -ArgumentList ''-ExecutionPolicy Bypass -File "{0}"''' -f $PSCommandPath)
    exit 1
}

Write-Host @'
==========================================================
 Reverse SSH bootstrap (Windows)
 local PC  ->  remote server  ->  (reverse tunnel)  -> local PC
==========================================================
This will modify:
  - OpenSSH Server install / sshd service / sshd_config
  - %USERPROFILE%\.ssh\{config,authorized_keys,claude_tunnel_ed25519}
All modified system files are backed up first.
'@

# ---------------------------------------------------------------- inputs
if (-not $ServerHost)  { $ServerHost  = Read-Default 'Remote server hostname / IP' }
if (-not $ServerHost)  { Write-Err 'Server hostname must not be empty'; exit 1 }
if (-not $ServerUser)  { $ServerUser  = Read-Default 'Remote server SSH user' }
if (-not $ServerUser)  { Write-Err 'Server user must not be empty'; exit 1 }
if ($ServerPort -le 0) { $ServerPort  = [int](Read-Default 'Remote server SSH port' '22') }
if ($ReversePort -le 0){ $ReversePort = [int](Read-Default 'Reverse SSH port on the server (used by Claude/Codex to connect back)' '2222') }
if (-not $LocalUser)   { $LocalUser   = Read-Default 'Local username (used when connecting back from the server)' $env:USERNAME }

Write-Host ''
Write-Host 'Server-side public key: the .pub of the key that Claude / Codex on the'
Write-Host 'server will use to SSH back into this machine.'
Write-Host "(paste the whole line, e.g. 'ssh-ed25519 AAAA... comment'; leave empty to skip)"
if (-not $ServerPublicKey) { $ServerPublicKey = Read-Default 'Server-side public key' '' }

$DisablePassword = Read-YesNo 'Disable password login for the local sshd (recommended, public key only)' $true
$LoopbackOnly    = Read-YesNo 'Make the local sshd listen on 127.0.0.1 only (recommended; note: direct SSH from the LAN will stop working)' $true

# ---------------------------------------------------------------- OpenSSH Server install
Write-Info 'Checking whether OpenSSH Server is installed'
$cap = Get-WindowsCapability -Online | Where-Object Name -like 'OpenSSH.Server*' | Select-Object -First 1
if (-not $cap) {
    Write-Err 'OpenSSH.Server capability not found; Windows 10 1809+ / Windows 11 is required.'
    exit 1
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

# ---------------------------------------------------------------- start sshd + auto start
# Start once first so OpenSSH generates the default sshd_config and host keys
# under %ProgramData%\ssh
Write-Info 'Starting the sshd service and enabling auto-start'
Set-Service -Name sshd -StartupType Automatic
Start-Service -Name sshd

# ---------------------------------------------------------------- sshd_config
if (-not (Test-Path $SshdConfig)) {
    Write-Err "$SshdConfig not found (it should be generated on the first sshd start)"
    exit 1
}
$backupPath = "$SshdConfig.claude-bak-$Ts"
Copy-Item $SshdConfig $backupPath
Write-Info "Backed up sshd_config -> $backupPath"

$raw = Get-Content -Raw $SshdConfig

# 1) Comment out the administrators_authorized_keys Match block so users in
#    the Administrators group also use their own
#    %USERPROFILE%\.ssh\authorized_keys.
#    Already-commented lines no longer match the pattern, so re-runs are
#    idempotent.
$raw = $raw -replace '(?m)^([ \t]*Match[ \t]+Group[ \t]+administrators[ \t]*)\r?$', '# claude-bootstrap disabled: $1'
$raw = $raw -replace '(?m)^([ \t]*AuthorizedKeysFile[ \t]+__PROGRAMDATA__[/\\]ssh[/\\]administrators_authorized_keys[ \t]*)\r?$', '# claude-bootstrap disabled: $1'

# 2) Set global directives (replace the first match only; append to the end
#    of the file when not found)
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

$raw = Set-SshdDirective $raw 'PubkeyAuthentication' 'yes'
$raw = Set-SshdDirective $raw 'AuthorizedKeysFile' '.ssh/authorized_keys'
if ($DisablePassword) {
    $raw = Set-SshdDirective $raw 'PasswordAuthentication' 'no'
}
if ($LoopbackOnly) {
    $raw = Set-SshdDirective $raw 'ListenAddress' '127.0.0.1'
}

[System.IO.File]::WriteAllText($SshdConfig, $raw)

Write-Info 'Validating the sshd config (sshd -t)'
# sshd -t reports errors on stderr; with EAP=Stop, 2>&1 wraps stderr into an
# exception (PS 5.1 NativeCommandError), so relax it during validation to make
# sure the backup gets restored on failure
$prevEap = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$sshdCheck = & $SshdExe -t 2>&1
$sshdCheckCode = $LASTEXITCODE
$ErrorActionPreference = $prevEap
$sshdCheck | ForEach-Object { Write-Host "    $_" }
if ($sshdCheckCode -ne 0) {
    Write-Err 'sshd config validation failed, restoring the backup'
    Copy-Item $backupPath $SshdConfig -Force
    exit 1
}
Write-Info 'Config validation passed, restarting sshd'
Restart-Service -Name sshd

# ---------------------------------------------------------------- ~/.ssh + ACL
Write-Info 'Preparing %USERPROFILE%\.ssh and tightening its ACLs'
if (-not (Test-Path $SshDir)) { New-Item -ItemType Directory -Path $SshDir | Out-Null }
if (-not (Test-Path $AuthKeys)) { New-Item -ItemType File -Path $AuthKeys | Out-Null }
Set-StrictAcl -Path $SshDir -Directory
Set-StrictAcl -Path $AuthKeys

# ---------------------------------------------------------------- server pubkey -> authorized_keys
if ($ServerPublicKey) {
    $tmp = [System.IO.Path]::GetTempFileName()
    try {
        [System.IO.File]::WriteAllText($tmp, $ServerPublicKey + "`n")
        $prevEap = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        & $SshKeygenExe -lf $tmp *> $null
        $keyCheckCode = $LASTEXITCODE
        $ErrorActionPreference = $prevEap
        if ($keyCheckCode -ne 0) {
            Write-Err 'The pasted content is not a valid SSH public key; please check and re-run'
            exit 1
        }
    } finally {
        Remove-Item $tmp -ErrorAction SilentlyContinue
    }
    $blob = ($ServerPublicKey -split '\s+' | Where-Object { $_ -like 'AAAA*' } | Select-Object -First 1)
    if (-not $blob) { Write-Err 'Could not parse the key data from the public key'; exit 1 }
    $existing = Get-Content -Raw $AuthKeys -ErrorAction SilentlyContinue
    if ($existing -and $existing.Contains($blob)) {
        Write-Info 'This public key is already in authorized_keys, skipping'
    } else {
        $entry = "from=`"127.0.0.1,::1`",no-agent-forwarding,no-X11-forwarding $ServerPublicKey"
        Add-Content -Path $AuthKeys -Value $entry -Encoding ascii
        Write-Info 'Written to authorized_keys (restricted to loopback logins only)'
    }
} else {
    Write-Warn "No server-side public key provided. You can append it to $AuthKeys later,"
    Write-Warn 'recommended format: from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding <public-key>'
}

# ---------------------------------------------------------------- local tunnel key
# Prefer an existing key over generating yet another one: a dedicated key
# from a previous run first, then the user's default id_ed25519 (opt-in).
$DefaultKey = Join-Path $SshDir 'id_ed25519'
if ((Test-Path $KeyPath) -and (Test-Path "$KeyPath.pub")) {
    Write-Info "Local tunnel key already exists: $KeyPath"
} elseif ((Test-Path $DefaultKey) -and (Test-Path "$DefaultKey.pub") -and
          (Read-YesNo "Found $DefaultKey — use it for the tunnel instead of generating a dedicated key" $true)) {
    $KeyPath = $DefaultKey
    $KeyName = 'id_ed25519'
    Write-Warn 'If this key has a passphrase, tunnel autostart will need an ssh-agent to work'
} else {
    Write-Info "Generating the SSH key used to connect to the server: $KeyPath"
    & $SshKeygenExe -t ed25519 -f $KeyPath -N '""' -C 'claude-tunnel' | Out-Null
    if ($LASTEXITCODE -ne 0) { Write-Err 'ssh-keygen failed'; exit 1 }
}
Set-StrictAcl -Path $KeyPath

# ---------------------------------------------------------------- ~/.ssh/config
$configBlock = @"
$BeginMark
Host $TunnelAlias
    HostName $ServerHost
    User $ServerUser
    Port $ServerPort
    IdentityFile ~/.ssh/$KeyName
    IdentitiesOnly yes
    RemoteForward 127.0.0.1:$ReversePort 127.0.0.1:22
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
            Write-Err "Aborted. Please remove the old Host $TunnelAlias block and re-run"
            exit 1
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

# ---------------------------------------------------------------- copy key to server (optional)
Write-Host ''
Write-Info "Local tunnel public key (add it to ~/.ssh/authorized_keys of $ServerUser on the server):"
Write-Host ''
Get-Content "$KeyPath.pub" | Write-Host
Write-Host ''
if (Read-YesNo 'Upload it to the server now (ssh-copy-id equivalent, needs the server password or existing working auth)' $false) {
    $pub = (Get-Content "$KeyPath.pub" -Raw).Trim()
    $remoteCmd = "mkdir -p ~/.ssh && chmod 700 ~/.ssh && grep -qF '$pub' ~/.ssh/authorized_keys 2>/dev/null || echo '$pub' >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys"
    & $SshExe -p $ServerPort "$ServerUser@$ServerHost" $remoteCmd
    if ($LASTEXITCODE -eq 0) { Write-Info 'Uploaded' }
    else { Write-Warn 'Upload failed; please append the public key above to ~/.ssh/authorized_keys on the server manually' }
}

# ---------------------------------------------------------------- Scheduled Task (optional)
Write-Host ''
$autostart = Read-YesNo 'Register a Scheduled Task to start and keep the tunnel up after logon (optional)' $false
if ($autostart) {
    # keepalive script: reconnect 15 seconds after ssh exits
    $keepAlive = @"
`$ssh = '$SshExe'
while (`$true) {
    & `$ssh -N -o ExitOnForwardFailure=yes $TunnelAlias
    Start-Sleep -Seconds 15
}
"@
    [System.IO.File]::WriteAllText($KeepAlivePs1, $keepAlive)
    Set-StrictAcl -Path $KeepAlivePs1

    $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
        -Argument "-NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File `"$KeepAlivePs1`""
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
        -StartWhenAvailable -MultipleInstances IgnoreNew `
        -ExecutionTimeLimit (New-TimeSpan -Seconds 0) `
        -RestartCount 10 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
        -Settings $settings -Description 'Keep the remote-claude reverse SSH tunnel alive' -Force | Out-Null
    Write-Info "Scheduled Task registered: $TaskName (starts automatically at next logon)"
    if (Read-YesNo 'Start the Scheduled Task now' $true) {
        Start-ScheduledTask -TaskName $TaskName
        Write-Info 'Scheduled Task started'
    }
}

# ---------------------------------------------------------------- summary
Write-Host @"

==========================================================
 Done! Next steps
==========================================================
1. Make sure the local tunnel public key is added on the server
   (~$ServerUser/.ssh/authorized_keys):
     $KeyPath.pub

2. Start the tunnel manually (keeps running in the foreground):
     ssh -N $TunnelAlias

3. While the tunnel stays connected, Claude / Codex on the server can use:
     ssh -i ~/.ssh/claude_to_local_ed25519 -p $ReversePort $LocalUser@127.0.0.1
   (point -i at the actual private key path of the connect-back key on the server)

   Tip: run server/setup-server.sh on the server to install the
   my-device ssh alias and the claude-local-shell SHELL wrapper.
"@
if ($autostart) {
    Write-Host @"
The tunnel is set to start at logon (Scheduled Task $TaskName). To stop it:
     Stop-ScheduledTask -TaskName $TaskName
     Unregister-ScheduledTask -TaskName $TaskName -Confirm:`$false
"@
} else {
    Write-Host 'For start-at-logon, re-run this script and answer yes at the Scheduled Task step.'
}
Write-Host ''
Write-Host "See the 'Removal / rollback' section of README.md for rollback instructions."
