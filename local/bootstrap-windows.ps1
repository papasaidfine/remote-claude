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
    6. xray client: install xray, seed vless-nodes.txt (one vless:// URL per
       line) and write the per-connection ProxyCommand launcher; every
       connection picks a random node from that file.
    7. Toggle routing the tunnel through the xray proxy - rewrites the
       managed block reusing its stored values, no re-prompting. Each ssh
       connection then runs its own xray, which dies with the connection.

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
    [string]$ServerPublicKey,
    [string]$VlessUrl,
    [ValidateSet('', '0', '1')][string]$UseXrayProxy = ''
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
$VlessNodes   = Join-Path $RcConfigDir 'vless-nodes.txt'

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

function Read-VlessNodes { # node URLs from a nodes file; comments and blanks dropped
    param([string]$Path)
    $nodes = @()
    if (Test-Path $Path) {
        foreach ($line in @(Get-Content $Path)) {
            $t = "$line".Trim()
            if (-not $t -or $t.StartsWith('#')) { continue }
            $nodes += $t
        }
    }
    return ,$nodes
}

function Resolve-XrayExe { # path to an xray binary, or $null
    if (Test-Path $XrayVendorBin) { return $XrayVendorBin }
    $cmd = Get-Command xray.exe -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    return $null
}

function Install-Xray {
    $existing = Resolve-XrayExe
    if ($existing) { Write-Info "xray already available: $existing"; return }
    $asset = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'Xray-windows-arm64-v8a.zip' } else { 'Xray-windows-64.zip' }
    $binDir = Split-Path $XrayVendorBin
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    $zip = Join-Path $env:TEMP "xray-dl-$Ts.zip"
    Write-Info "Downloading $asset (github.com/XTLS/Xray-core)"
    $prevPp = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'
    try {
        Invoke-WebRequest -UseBasicParsing -OutFile $zip `
            -Uri "https://github.com/XTLS/Xray-core/releases/latest/download/$asset"
    } finally { $ProgressPreference = $prevPp }
    Expand-Archive -Path $zip -DestinationPath $binDir -Force
    Remove-Item $zip -ErrorAction SilentlyContinue
    if (-not (Test-Path $XrayVendorBin)) { throw 'xray.exe missing after extraction' }
    Write-Info "xray installed to $XrayVendorBin"
}

function Write-XrayLauncher {
    New-Item -ItemType Directory -Force -Path $RcConfigDir | Out-Null
    $head = @'
# Auto-generated by bootstrap-windows.ps1 - per-connection xray for ssh.
# Usage (from ssh_config): ProxyCommand powershell.exe ... -File "<this>" %h %p
# Picks a RANDOM node from vless-nodes.txt (one vless:// URL per line,
# # comments), starts a private xray (dokodemo-door -> DestHost:DestPort) on a
# free port, bridges stdio to it, and guarantees xray dies with this process
# via a kill-on-close Job Object (survives ssh's TerminateProcess teardown).
param(
    [Parameter(Mandatory = $true)][string]$DestHost,
    [Parameter(Mandatory = $true)][int]$DestPort
)
$ErrorActionPreference = 'Stop'
$RcDir = Join-Path $env:LOCALAPPDATA 'remote-claude'

Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class RcJob {
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern IntPtr CreateJobObject(IntPtr attrs, string name);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool SetInformationJobObject(IntPtr job, int cls, IntPtr info, int len);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
    [DllImport("kernel32.dll")]
    static extern IntPtr GetCurrentProcess();

    [StructLayout(LayoutKind.Sequential)]
    struct BASIC_LIMITS {
        public long PerProcessUserTimeLimit; public long PerJobUserTimeLimit;
        public uint LimitFlags; public UIntPtr MinWorkingSet; public UIntPtr MaxWorkingSet;
        public uint ActiveProcessLimit; public UIntPtr Affinity;
        public uint PriorityClass; public uint SchedulingClass;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct IO_COUNTERS {
        public ulong ReadOps; public ulong WriteOps; public ulong OtherOps;
        public ulong ReadBytes; public ulong WriteBytes; public ulong OtherBytes;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct EXTENDED_LIMITS {
        public BASIC_LIMITS Basic; public IO_COUNTERS Io;
        public UIntPtr ProcessMemoryLimit; public UIntPtr JobMemoryLimit;
        public UIntPtr PeakProcessMemory; public UIntPtr PeakJobMemory;
    }
    // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000; info class 9 = ExtendedLimits
    public static void BindCurrentProcess() {
        IntPtr job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero) throw new InvalidOperationException("CreateJobObject failed");
        var limits = new EXTENDED_LIMITS();
        limits.Basic.LimitFlags = 0x2000;
        int len = Marshal.SizeOf(typeof(EXTENDED_LIMITS));
        IntPtr ptr = Marshal.AllocHGlobal(len);
        try {
            Marshal.StructureToPtr(limits, ptr, false);
            if (!SetInformationJobObject(job, 9, ptr, len))
                throw new InvalidOperationException("SetInformationJobObject failed");
        } finally { Marshal.FreeHGlobal(ptr); }
        if (!AssignProcessToJobObject(job, GetCurrentProcess()))
            throw new InvalidOperationException("AssignProcessToJobObject failed");
        // The job handle is intentionally never closed: it lives exactly as
        // long as this process, which is the point.
    }
}
"@
[RcJob]::BindCurrentProcess()
'@

    $body = @'
$xrayExe = Join-Path (Join-Path $RcDir 'bin') 'xray.exe'
if (-not (Test-Path $xrayExe)) {
    $cmd = Get-Command xray.exe -ErrorAction SilentlyContinue
    if ($cmd) { $xrayExe = $cmd.Source }
    else { [Console]::Error.WriteLine('xray.exe not found - re-run bootstrap item 6'); exit 1 }
}

# Random node for THIS connection
$nodesFile = Join-Path $RcDir 'vless-nodes.txt'
$nodes = Read-VlessNodes $nodesFile
if ($nodes.Count -eq 0) {
    [Console]::Error.WriteLine("no vless:// nodes in $nodesFile - re-run bootstrap item 6")
    exit 1
}
$node = $nodes | Get-Random

# Free loopback port for this connection's private xray
$probe = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 0)
$probe.Start()
$dokoPort = ([System.Net.IPEndPoint]$probe.LocalEndpoint).Port
$probe.Stop()

$logFile = (Join-Path $env:TEMP "rc-xray-$PID.log") -replace '\\', '/'
$conf = ConvertTo-VlessJson $node
$conf = $conf.Replace('"__DOKO_PORT__"', "$dokoPort")
$conf = $conf.Replace('"__DEST_PORT__"', "$DestPort")
$conf = $conf.Replace('__DEST_HOST__', $DestHost)
$conf = $conf.Replace('__LOG_FILE__', $logFile)
$name = if ($node -match '#(.+)$') { $Matches[1] } else { '(unnamed)' }
[System.IO.File]::AppendAllText($logFile, "chosen node: $name`r`n")
$tmpConf = Join-Path $env:TEMP "rc-xray-$PID.json"
[System.IO.File]::WriteAllText($tmpConf, $conf)

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $xrayExe
$psi.Arguments = 'run -c "{0}"' -f $tmpConf
$psi.UseShellExecute = $false
$psi.CreateNoWindow = $true
$xray = [System.Diagnostics.Process]::Start($psi)

# Wait for the inbound; the successful probe becomes the bridge connection
# (dokodemo dials DestHost:DestPort on accept, which is what we want).
$client = $null
for ($i = 0; $i -lt 25 -and -not $client; $i++) {
    if ($xray.HasExited) { break }
    $c = New-Object System.Net.Sockets.TcpClient
    try { $c.Connect('127.0.0.1', $dokoPort); $client = $c }
    catch { $c.Close(); Start-Sleep -Milliseconds 200 }
}
if (-not $client) {
    [Console]::Error.WriteLine("xray did not come up; see $logFile")
    try { if (-not $xray.HasExited) { $xray.Kill() } } catch {}
    Remove-Item $tmpConf -ErrorAction SilentlyContinue
    exit 1
}

try {
    $net = $client.GetStream()
    $stdin  = [Console]::OpenStandardInput()
    $stdout = [Console]::OpenStandardOutput()
    $up   = $stdin.CopyToAsync($net)
    $down = $net.CopyToAsync($stdout)
    [System.Threading.Tasks.Task]::WaitAny(@($up, $down)) | Out-Null
    try { $stdout.Flush() } catch {}
} finally {
    try { $client.Close() } catch {}
    try { if (-not $xray.HasExited) { $xray.Kill() } } catch {}
    Remove-Item $tmpConf, $logFile -ErrorAction SilentlyContinue
}
'@

    # Single-source: serialize the bootstrap's own functions into the launcher
    $launcher = $head + "`r`n" +
        'function ConvertTo-VlessJson {' + [string]${function:ConvertTo-VlessJson} + "}`r`n" +
        'function Read-VlessNodes {' + [string]${function:Read-VlessNodes} + "}`r`n" +
        $body
    [System.IO.File]::WriteAllText($XrayLauncher, $launcher)
    Write-Info "Wrote $XrayLauncher"
}

function Invoke-ItemXray {   # item 6: install xray + seed/validate nodes file + launcher
    $nodes = Read-VlessNodes $VlessNodes
    if ($nodes.Count -gt 0) {
        for ($i = 0; $i -lt $nodes.Count; $i++) {
            try { ConvertTo-VlessJson $nodes[$i] | Out-Null }
            catch { throw "Node $($i + 1) in $VlessNodes does not parse: $_" }
        }
        Write-Info "Validated $($nodes.Count) node(s) from $VlessNodes"
    } else {
        $url = $VlessUrl
        if (-not $url) { $url = Read-Default 'Paste your vless:// URL' }
        if (-not $url) { throw 'No URL given; nothing changed' }
        ConvertTo-VlessJson $url | Out-Null    # throws before any side effect
        New-Item -ItemType Directory -Force -Path $RcConfigDir | Out-Null
        $seed = @(
            '# vless nodes for the remote-claude tunnel - one vless:// URL per line.',
            '# Lines starting with # and blank lines are ignored.',
            '# Every connection picks a random node; edits take effect on the next connect.',
            $url
        ) -join "`r`n"
        [System.IO.File]::WriteAllText($VlessNodes, $seed + "`r`n")
        Write-Info "Wrote $VlessNodes"
    }
    Install-Xray
    Write-XrayLauncher
    Remove-Item $XrayJson -Force -ErrorAction SilentlyContinue  # pre-nodes-file layout
    Write-Info "Each connection picks a random node from $VlessNodes - edit that file to add/swap nodes."
    Write-Info 'xray client ready. Turn it on for the tunnel via menu item 7 (proxy toggle).'
}

function Invoke-ItemProxy {  # item 7: toggle routing the tunnel through xray
    if (-not (Test-StatusConfig)) { throw "No managed Host $TunnelAlias block yet - run item 4 first" }
    $srvHost = Get-ConfigBlockValue 'HostName'
    $srvUser = Get-ConfigBlockValue 'User'
    $srvPort = Get-ConfigBlockValue 'Port'
    $revPort = ((Get-ConfigBlockValue 'RemoteForward') -split ':')[-1]
    if (-not ($srvHost -and $srvUser -and $srvPort -and $revPort)) {
        throw "Could not read the Host $TunnelAlias block - re-run item 4"
    }
    if (Test-ConfigProxyOn) {
        Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort ([int]$srvPort) -RevPort ([int]$revPort) -Force
        Write-Info "Proxy OFF - ssh $TunnelAlias connects directly again"
    } else {
        if (-not (Test-StatusXray)) { throw 'xray client not configured - run item 6 first' }
        Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort ([int]$srvPort) -RevPort ([int]$revPort) -UseProxy -Force
        Write-Info "Proxy ON - ssh $TunnelAlias now routes through xray"
    }
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

# Write (or rewrite) the managed Host block. -UseProxy adds the xray
# ProxyCommand line; -Force skips the "update it?" confirmation (used by the
# item-7 toggle, which has already collected its answer).
function Write-SshConfigBlock {
    param(
        [string]$SrvHost, [string]$SrvUser, [int]$SrvPort, [int]$RevPort,
        [switch]$UseProxy, [switch]$Force
    )
    $lines = @(
        $BeginMark
        "Host $TunnelAlias"
        "    HostName $SrvHost"
        "    User $SrvUser"
        "    Port $SrvPort"
        "    IdentityFile ~/.ssh/$KeyName"
        "    IdentitiesOnly yes"
    )
    if ($UseProxy) {
        $lines += "    ProxyCommand powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"$XrayLauncher`" %h %p"
    }
    $lines += @(
        "    RemoteForward 127.0.0.1:$RevPort 127.0.0.1:22"
        "    ExitOnForwardFailure yes"
        "    ServerAliveInterval 30"
        "    ServerAliveCountMax 3"
        "    ForwardAgent no"
        $EndMark
    )
    $configBlock = $lines -join "`r`n"

    if (-not (Test-Path $SshConfig)) { New-Item -ItemType File -Path $SshConfig | Out-Null }
    $configRaw = Get-Content -Raw $SshConfig -ErrorAction SilentlyContinue
    if ($null -eq $configRaw) { $configRaw = '' }

    if ($configRaw.Contains($BeginMark)) {
        if (-not $Force) {
            if (-not (Read-YesNo "~\.ssh\config already contains a $TunnelAlias block, update it" $true)) {
                Write-Warn 'Keeping the existing block, skipping the write'
                return
            }
        }
        Copy-Item $SshConfig "$SshConfig.claude-bak-$Ts"
        Write-Info "Backed up ssh config -> $SshConfig.claude-bak-$Ts"
        $escBegin = [regex]::Escape($BeginMark)
        $escEnd   = [regex]::Escape($EndMark)
        $configRaw = [regex]::Replace($configRaw, "(?s)$escBegin.*?$escEnd(\r?\n)?", '')
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

    $configRaw = $configRaw.TrimEnd()
    if ($configRaw) { $configRaw += "`r`n`r`n" }
    $configRaw += $configBlock + "`r`n"
    [System.IO.File]::WriteAllText($SshConfig, $configRaw)
    Write-Info "Wrote Host $TunnelAlias to $SshConfig"
    Set-StrictAcl -Path $SshConfig
}

function Get-ConfigBlockValue { # Get-ConfigBlockValue <Key> -> value inside the managed block
    param([string]$Key)
    $raw = Get-Content -Raw $SshConfig -ErrorAction SilentlyContinue
    if (-not $raw) { return '' }
    $m = [regex]::Match($raw, "(?s)$([regex]::Escape($BeginMark))(.*?)$([regex]::Escape($EndMark))")
    if (-not $m.Success) { return '' }
    $mm = [regex]::Match($m.Groups[1].Value, "(?m)^[ \t]+$([regex]::Escape($Key))[ \t]+(\S+)")
    if ($mm.Success) { return $mm.Groups[1].Value }
    return ''
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

    $useProxy = $false
    if (Test-StatusXray) {
        if ($UseXrayProxy -ne '') { $useProxy = ($UseXrayProxy -eq '1') }
        elseif (Read-YesNo 'Route this tunnel through the local xray proxy' $true) { $useProxy = $true }
    }
    Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort $srvPort -RevPort $revPort -UseProxy:$useProxy
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
function Test-StatusXray {
    return ((Read-VlessNodes $VlessNodes).Count -gt 0) -and (Test-Path $XrayLauncher) -and [bool](Resolve-XrayExe)
}
function Test-ConfigProxyOn {
    $raw = Get-Content -Raw $SshConfig -ErrorAction SilentlyContinue
    return [bool]($raw -and $raw.Contains('ProxyCommand') -and $raw.Contains($XrayLauncher))
}

# ---------------------------------------------------------------- menu
function Format-Mark { param([bool]$Ok) if ($Ok) { '[done]' } else { '[ -  ]' } }

function Show-Menu {
    Write-Host ''
    Write-Host '----------------------------------------------------------'
    Write-Host ('  1) {0,-50} {1}' -f 'Incoming SSH - OpenSSH Server + harden  [admin]', (Format-Mark (Test-StatusSshd)))
    Write-Host ('  2) {0,-50} {1}' -f 'Local SSH key (~\.ssh\id_ed25519)', (Format-Mark (Test-StatusKey)))
    Write-Host ('  3) {0,-50} {1}' -f "Authorize the server's connect-back key", (Format-Mark (Test-StatusAuthorize)))
    $cfgLabel = 'Tunnel config (Host remote-claude)'
    if (Test-ConfigProxyOn) { $cfgLabel += ' [xray]' }
    Write-Host ('  4) {0,-50} {1}' -f $cfgLabel, (Format-Mark (Test-StatusConfig)))
    Write-Host  '  5) Show local public key (paste into server setup)'
    Write-Host ('  6) {0,-50} {1}' -f 'xray client (vless-nodes.txt)', (Format-Mark (Test-StatusXray)))
    Write-Host ('  7) {0,-50} {1}' -f 'Route tunnel through xray (ProxyCommand)', (Format-Mark (Test-ConfigProxyOn)))
    Write-Host  '  q) Quit'
}

if (-not $env:RC_SOURCED_FOR_TEST) {
    :menu while ($true) {
        Show-Menu
        $choice = (Read-Host 'Select [1-7, q]').Trim()
        if ($choice -match '^[Qq]$') { break menu }
        $fn = switch ($choice) {
            '1' { 'Invoke-ItemSshd' }
            '2' { 'Invoke-ItemKey' }
            '3' { 'Invoke-ItemAuthorize' }
            '4' { 'Invoke-ItemConfig' }
            '5' { 'Invoke-ItemShowKey' }
            '6' { 'Invoke-ItemXray' }
            '7' { 'Invoke-ItemProxy' }
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
