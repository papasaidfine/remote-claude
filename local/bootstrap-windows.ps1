<#
.SYNOPSIS
  Reverse SSH dev-environment bootstrap for Windows.

.DESCRIPTION
  Prepares this Windows machine so a remote server's Claude / Codex agent can
  SSH back into it through a reverse tunnel:

    local PC  -- ssh remote-claude -->  remote server
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
    4. Write the base "Host remote-claude" block into
       %USERPROFILE%\.ssh\config (host/user/port only; keeps an existing
       reverse port / proxy).
    5. Add or update the reverse tunnel port (RemoteForward) on that block.
    6. xray client: ask for an optional download proxy, then download the xray
       binary (or version-check and update it) and write the per-connection
       ProxyCommand launcher; nodes live in vless-nodes.txt (one vless:// URL
       per line, a random one per connection).
    7. Toggle routing the tunnel through the xray proxy (ProxyCommand). Each
       ssh connection then runs its own xray, which dies with the connection.
    8. Show the local public key to paste into the server-side setup.

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
$XrayRelay    = Join-Path (Join-Path $RcConfigDir 'bin') 'rc-stdio-relay.exe'
$VlessNodes   = Join-Path $RcConfigDir 'vless-nodes.txt'
$DlProxy      = ''        # optional proxy for the GitHub downloads; item 6 asks each run

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
    return "$(Read-Host $Prompt)".Trim()
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
# placeholders that xray-proxy.ps1 fills in per connection. The VLESS outbound
# is fully resolved.
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
        log = [ordered]@{ loglevel = 'warning' }
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

function Read-DlProxy { # item 6: optional proxy for the GitHub downloads (empty = direct)
    $script:DlProxy = Read-Default 'Proxy for the xray download (e.g. http://127.0.0.1:7890, empty = direct)'
    if ($script:DlProxy) { Write-Info "Using proxy $script:DlProxy for this item's downloads" }
}

function Get-ProxyArgs { # splat for Invoke-WebRequest / Invoke-RestMethod
    if ($script:DlProxy) { return @{ Proxy = $script:DlProxy } }
    return @{}
}

function Install-Xray {
    $existing = Resolve-XrayExe
    if ($existing) { Write-Info "xray already available: $existing"; return }
    Install-XrayRelease
}

function Install-XrayRelease { # download the latest release binary into the vendor path
    $asset = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'Xray-windows-arm64-v8a.zip' } else { 'Xray-windows-64.zip' }
    $binDir = Split-Path $XrayVendorBin
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    $zip = Join-Path $env:TEMP "xray-dl-$Ts.zip"
    Write-Info "Downloading $asset (github.com/XTLS/Xray-core)"
    $prevPp = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue'
    $proxyArgs = Get-ProxyArgs
    try {
        Invoke-WebRequest -UseBasicParsing -OutFile $zip -TimeoutSec 30 @proxyArgs `
            -Uri "https://github.com/XTLS/Xray-core/releases/latest/download/$asset"
    } finally { $ProgressPreference = $prevPp }
    Expand-Archive -Path $zip -DestinationPath $binDir -Force
    Remove-Item $zip -ErrorAction SilentlyContinue
    if (-not (Test-Path $XrayVendorBin)) { throw 'xray.exe missing after extraction' }
    Write-Info "xray installed to $XrayVendorBin"
}

function Get-XrayLocalVersion { # e.g. 25.0.0; '' when unknown
    param([string]$Exe)
    try {
        $line = (& $Exe version 2>$null | Select-Object -First 1)
        if ("$line" -match 'Xray (\S+)') { return $Matches[1] }
    } catch {}
    return ''
}

function Get-XrayLatestVersion { # latest release tag, 'v' stripped; '' on failure
    try {
        $proxyArgs = Get-ProxyArgs
        $r = Invoke-RestMethod -UseBasicParsing -TimeoutSec 10 @proxyArgs `
            -Uri 'https://api.github.com/repos/XTLS/Xray-core/releases/latest'
        return ("$($r.tag_name)" -replace '^v', '')
    } catch { return '' }
}

function Update-XrayBinary { # version-check the resolved binary; refresh the vendor copy when stale
    $exe = Resolve-XrayExe
    if (-not $exe) { return }
    $cur = Get-XrayLocalVersion $exe
    if (-not $cur) { $cur = 'unknown' }
    $latest = Get-XrayLatestVersion
    if (-not $latest) { Write-Warn "Could not check the latest xray version (GitHub unreachable); keeping $cur" }
    elseif ($cur -eq $latest) { Write-Info "xray $cur is up to date" }
    elseif ($exe -eq $XrayVendorBin) { Write-Info "Updating xray $cur -> $latest"; Install-XrayRelease }
    else { Write-Warn "xray at $exe is $cur (latest: $latest) - it was installed outside this script; update it yourself." }
}

function Ensure-VlessNodesFile { # create the nodes file if missing ($VlessUrl seeds the first line)
    if (Test-Path $VlessNodes) { return }
    if ($VlessUrl) { ConvertTo-VlessJson $VlessUrl | Out-Null }  # throws on a bad URL
    New-Item -ItemType Directory -Force -Path $RcConfigDir | Out-Null
    $lines = @(
        '# vless nodes for the remote-claude tunnel - one vless:// URL per line.',
        '# Lines starting with # and blank lines are ignored.',
        '# Every xray start picks a random node; edits take effect on the next connect.'
    )
    if ($VlessUrl) { $lines += $VlessUrl }
    [System.IO.File]::WriteAllText($VlessNodes, ($lines -join "`r`n") + "`r`n")
    Write-Info "Created $VlessNodes"
}

function Write-XrayLauncher {
    New-Item -ItemType Directory -Force -Path $RcConfigDir | Out-Null
    $head = @'
# Auto-generated by bootstrap-windows.ps1 - per-connection xray for ssh.
# Called by rc-stdio-relay.exe to materialize one per-connection xray config.
# Picks a RANDOM node from vless-nodes.txt (one vless:// URL per line,
# # comments). The native relay owns xray and the SSH byte stream.
param(
    [Parameter(Mandatory = $true)][string]$DestHost,
    [Parameter(Mandatory = $true)][int]$DestPort,
    [switch]$PrepareOnly,
    [int]$PreparePort = 0,
    [string]$PrepareConfig
)
$ErrorActionPreference = 'Stop'
$RcDir = Join-Path $env:LOCALAPPDATA 'remote-claude'
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
    [Console]::Error.WriteLine("no vless:// nodes in $nodesFile - edit the file and add one")
    exit 1
}
$node = $nodes | Get-Random

# The native relay supplies all paths and the port. The legacy branch remains
# only so an old ProxyCommand fails cleanly until item 6 migrates its config.
if ($PrepareOnly) {
    if ($PreparePort -lt 1 -or -not $PrepareConfig) {
        throw 'PrepareOnly requires PreparePort and PrepareConfig'
    }
    $dokoPort = $PreparePort
} else {
    [Console]::Error.WriteLine('legacy PowerShell ProxyCommand is unsupported; re-run bootstrap item 6 to install the native relay')
    exit 1
}

$conf = ConvertTo-VlessJson $node
$conf = $conf.Replace('"__DOKO_PORT__"', "$dokoPort")
$conf = $conf.Replace('"__DEST_PORT__"', "$DestPort")
$conf = $conf.Replace('__DEST_HOST__', $DestHost)
$tmpConf = $PrepareConfig
[System.IO.File]::WriteAllText($tmpConf, $conf)

exit 0
'@

    # Single-source: serialize the bootstrap's own functions into the launcher
    $launcher = $head + "`r`n" +
        'function ConvertTo-VlessJson {' + [string]${function:ConvertTo-VlessJson} + "}`r`n" +
        'function Read-VlessNodes {' + [string]${function:Read-VlessNodes} + "}`r`n" +
        $body
    [System.IO.File]::WriteAllText($XrayLauncher, $launcher)
    Write-Info "Wrote $XrayLauncher"
}

function Install-XrayRelay {
    $source = @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Runtime.InteropServices;
using System.Threading;

public static class RcStdioRelay {
    const int STD_INPUT_HANDLE = -10;
    const int STD_OUTPUT_HANDLE = -11;
    const uint JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000;
    static IntPtr jobHandle;

    [DllImport("kernel32.dll")] static extern IntPtr GetStdHandle(int handle);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool ReadFile(IntPtr handle, byte[] buffer, int count, out int read, IntPtr overlapped);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool WriteFile(IntPtr handle, IntPtr buffer, int count, out int written, IntPtr overlapped);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern IntPtr CreateJobObject(IntPtr attrs, string name);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool SetInformationJobObject(IntPtr job, int cls, IntPtr info, int len);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
    [DllImport("kernel32.dll")] static extern IntPtr GetCurrentProcess();

    [StructLayout(LayoutKind.Sequential)]
    struct BasicLimits {
        public long PerProcessUserTimeLimit, PerJobUserTimeLimit;
        public uint LimitFlags;
        public UIntPtr MinimumWorkingSetSize, MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public UIntPtr Affinity;
        public uint PriorityClass, SchedulingClass;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct IoCounters {
        public ulong ReadOperationCount, WriteOperationCount, OtherOperationCount;
        public ulong ReadTransferCount, WriteTransferCount, OtherTransferCount;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct ExtendedLimits {
        public BasicLimits BasicLimitInformation;
        public IoCounters IoInfo;
        public UIntPtr ProcessMemoryLimit, JobMemoryLimit, PeakProcessMemoryUsed, PeakJobMemoryUsed;
    }

    sealed class PumpState {
        public Socket Socket;
        public ManualResetEvent Done;
    }

    static void BindKillOnCloseJob() {
        jobHandle = CreateJobObject(IntPtr.Zero, null);
        if (jobHandle == IntPtr.Zero) return;
        ExtendedLimits limits = new ExtendedLimits();
        limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        int size = Marshal.SizeOf(typeof(ExtendedLimits));
        IntPtr data = Marshal.AllocHGlobal(size);
        try {
            Marshal.StructureToPtr(limits, data, false);
            if (!SetInformationJobObject(jobHandle, 9, data, size)) return;
        } finally { Marshal.FreeHGlobal(data); }
        AssignProcessToJobObject(jobHandle, GetCurrentProcess());
    }

    static string Quote(string value) { return "\"" + value.Replace("\"", "\\\"") + "\""; }

    static int ReservePort() {
        TcpListener listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        int port = ((IPEndPoint)listener.LocalEndpoint).Port;
        listener.Stop();
        return port;
    }

    static void PrepareConfig(string script, string host, int destinationPort, int inboundPort, string config) {
        string system = Environment.GetFolderPath(Environment.SpecialFolder.System);
        ProcessStartInfo psi = new ProcessStartInfo();
        psi.FileName = Path.Combine(system, @"WindowsPowerShell\v1.0\powershell.exe");
        psi.Arguments = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File " + Quote(script) +
            " " + Quote(host) + " " + destinationPort + " -PrepareOnly -PreparePort " + inboundPort +
            " -PrepareConfig " + Quote(config);
        psi.UseShellExecute = false;
        psi.CreateNoWindow = true;
        psi.RedirectStandardInput = true;
        psi.RedirectStandardOutput = true;
        psi.RedirectStandardError = true;
        using (Process process = Process.Start(psi)) {
            process.StandardInput.Close();
            string stdout = process.StandardOutput.ReadToEnd();
            string stderr = process.StandardError.ReadToEnd();
            process.WaitForExit();
            if (process.ExitCode != 0)
                throw new InvalidOperationException("xray config preparation failed: " + (stderr + stdout).Trim());
        }
    }

    static Process StartXray(string executable, string config) {
        ProcessStartInfo psi = new ProcessStartInfo();
        psi.FileName = executable;
        psi.Arguments = "run -c " + Quote(config);
        psi.UseShellExecute = false;
        psi.CreateNoWindow = true;
        psi.RedirectStandardInput = true;
        psi.RedirectStandardOutput = true;
        psi.RedirectStandardError = false;
        Process process = Process.Start(psi);
        process.StandardInput.Close();
        return process;
    }

    static Socket ConnectInbound(Process xray, int port) {
        for (int i = 0; i < 50; i++) {
            if (xray.HasExited) break;
            Socket socket = new Socket(AddressFamily.InterNetwork, SocketType.Stream, ProtocolType.Tcp);
            socket.NoDelay = true;
            try {
                socket.Connect(IPAddress.Loopback, port);
                return socket;
            } catch (SocketException) {
                socket.Close();
                Thread.Sleep(100);
            }
        }
        return null;
    }

    static void Upload(object value) {
        PumpState state = (PumpState)value;
        byte[] buffer = new byte[32768];
        IntPtr input = GetStdHandle(STD_INPUT_HANDLE);
        try {
            int count;
            while (ReadFile(input, buffer, buffer.Length, out count, IntPtr.Zero) && count > 0) {
                int offset = 0;
                while (offset < count) {
                    int sent = state.Socket.Send(buffer, offset, count - offset, SocketFlags.None);
                    if (sent <= 0) return;
                    offset += sent;
                }
            }
        } catch (Exception) { }
        finally { state.Done.Set(); }
    }

    static void Download(object value) {
        PumpState state = (PumpState)value;
        byte[] buffer = new byte[32768];
        IntPtr output = GetStdHandle(STD_OUTPUT_HANDLE);
        GCHandle pinned = GCHandle.Alloc(buffer, GCHandleType.Pinned);
        try {
            int count;
            while ((count = state.Socket.Receive(buffer)) > 0) {
                int offset = 0;
                while (offset < count) {
                    int written;
                    if (!WriteFile(output, IntPtr.Add(pinned.AddrOfPinnedObject(), offset), count - offset, out written, IntPtr.Zero) || written <= 0)
                        return;
                    offset += written;
                }
            }
        } catch (Exception) { }
        finally { pinned.Free(); state.Done.Set(); }
    }

    static void Pump(Socket socket) {
        ManualResetEvent done = new ManualResetEvent(false);
        PumpState state = new PumpState { Socket = socket, Done = done };
        Thread upload = new Thread(Upload), download = new Thread(Download);
        upload.IsBackground = true;
        download.IsBackground = true;
        upload.Start(state);
        download.Start(state);
        done.WaitOne();
    }

    public static int Main(string[] args) {
        if (args.Length != 2) { Console.Error.WriteLine("usage: rc-stdio-relay.exe host port"); return 2; }
        Process xray = null;
        Socket socket = null;
        string config = null;
        try {
            BindKillOnCloseJob();
            string bin = Path.GetDirectoryName(typeof(RcStdioRelay).Assembly.Location);
            string root = Directory.GetParent(bin).FullName;
            string script = Path.Combine(root, "xray-proxy.ps1");
            string xrayExe = Path.Combine(bin, "xray.exe");
            int pid = Process.GetCurrentProcess().Id;
            int port = ReservePort();
            config = Path.Combine(Path.GetTempPath(), "rc-xray-" + pid + ".json");
            PrepareConfig(script, args[0], Int32.Parse(args[1]), port, config);
            xray = StartXray(xrayExe, config);
            socket = ConnectInbound(xray, port);
            if (socket == null) throw new InvalidOperationException("xray did not come up");
            File.Delete(config);
            config = null;
            Pump(socket);
            return 0;
        } catch (Exception error) {
            Console.Error.WriteLine("remote-claude proxy: " + error.Message);
            return 1;
        } finally {
            if (socket != null) try { socket.Close(); } catch { }
            if (xray != null) {
                try { if (!xray.HasExited) xray.Kill(); } catch { }
                try { xray.WaitForExit(2000); } catch { }
                xray.Dispose();
            }
            if (config != null) try { File.Delete(config); } catch { }
        }
    }
}
'@
    $framework = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
    if (-not (Test-Path $framework)) {
        $framework = Join-Path $env:WINDIR 'Microsoft.NET\Framework\v4.0.30319\csc.exe'
    }
    if (-not (Test-Path $framework)) { throw 'The .NET Framework C# compiler was not found; Windows PowerShell 5.1 is required' }

    $binDir = Split-Path $XrayRelay
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    $tmpSource = Join-Path $env:TEMP "rc-stdio-relay-$PID.cs"
    $tmpExe = Join-Path $env:TEMP "rc-stdio-relay-$PID.exe"
    try {
        [System.IO.File]::WriteAllText($tmpSource, $source, (New-Object System.Text.UTF8Encoding($false)))
        & $framework /nologo /optimize+ /target:exe "/out:$tmpExe" $tmpSource
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path $tmpExe)) { throw 'Failed to compile rc-stdio-relay.exe' }
        Move-Item $tmpExe $XrayRelay -Force
    } finally {
        Remove-Item $tmpSource, $tmpExe -Force -ErrorAction SilentlyContinue
    }
    Write-Info "Wrote $XrayRelay"
}

function Invoke-ItemXray {   # item 6: download/update the xray binary + write the launcher
    $legacyProxy = $false
    if (Test-Path $SshConfig) {
        $configBefore = Get-Content -Raw $SshConfig -ErrorAction SilentlyContinue
        $legacyProxy = [bool]($configBefore -and $configBefore.Contains('ProxyCommand') -and $configBefore.Contains($XrayLauncher))
    }
    Read-DlProxy
    if (Resolve-XrayExe) { Update-XrayBinary } else { Install-Xray }
    Write-XrayLauncher
    Install-XrayRelay
    Ensure-VlessNodesFile
    Remove-Item $XrayJson -Force -ErrorAction SilentlyContinue  # pre-nodes-file layout
    Write-Info "Nodes file: $VlessNodes - one vless:// URL per line (# comments)."
    Write-Info 'Each connection picks a random node; edits take effect on the next connect.'
    if ($legacyProxy -and (Test-StatusConfig)) {
        $srvHost = Get-ConfigBlockValue 'HostName'
        $srvUser = Get-ConfigBlockValue 'User'
        $srvPort = Get-ConfigBlockValue 'Port'
        $rev = Get-ConfigBlockRport
        $revInt = if ($rev) { [int]$rev } else { 0 }
        if ($srvHost -and $srvUser -and $srvPort) {
            Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort ([int]$srvPort) -RevPort $revInt -UseProxy -Force
            Write-Info 'Migrated the legacy PowerShell ProxyCommand to rc-stdio-relay.exe'
        }
    }
    Write-Info 'Route the tunnel through xray via item 7 (ProxyCommand).'
}

function Invoke-ItemProxy {  # item 7: toggle ProxyCommand (route ssh through xray)
    if (-not (Test-StatusConfig)) { throw "No Host $TunnelAlias block yet - run item 4 first" }
    $srvHost = Get-ConfigBlockValue 'HostName'
    $srvUser = Get-ConfigBlockValue 'User'
    $srvPort = Get-ConfigBlockValue 'Port'
    if (-not ($srvHost -and $srvUser -and $srvPort)) { throw "Could not read the Host $TunnelAlias block - re-run item 4" }
    $rev = Get-ConfigBlockRport
    $revInt = if ($rev) { [int]$rev } else { 0 }
    $want = -not (Test-ConfigProxyOn)
    if ($UseXrayProxy -ne '') { $want = ($UseXrayProxy -eq '1') }
    if ($want) {
        if (-not (Test-StatusXray)) { throw 'xray client not set up - run item 6 first' }
        if ((Read-VlessNodes $VlessNodes).Count -eq 0) { throw "No nodes in $VlessNodes - add a vless:// URL there first" }
        Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort ([int]$srvPort) -RevPort $revInt -UseProxy -Force
        Write-Info "Proxy ON - ssh $TunnelAlias now routes through xray"
    } else {
        Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort ([int]$srvPort) -RevPort $revInt -Force
        Write-Info "Proxy OFF - ssh $TunnelAlias connects directly again"
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
        $lines += "    ProxyCommand `"$XrayRelay`" %h %p"
    }
    if ($RevPort -gt 0) {
        $lines += @(
            "    RemoteForward 127.0.0.1:$RevPort 127.0.0.1:22"
            "    ExitOnForwardFailure yes"
        )
    }
    $lines += @(
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

function Get-ConfigBlockRport { # reverse port from the managed block, '' when absent
    $rf = Get-ConfigBlockValue 'RemoteForward'
    if (-not $rf) { return '' }
    return ($rf -split ':')[-1]
}
function Test-StatusRport { return [bool](Get-ConfigBlockValue 'RemoteForward') }

function Test-ConfigFields { # $true when all fields are valid; else Write-Err + $false
    param([string]$SrvHost, [string]$SrvUser, [string]$SrvPort)
    if (-not $SrvHost) { Write-Err 'Server host must not be empty'; return $false }
    if (-not $SrvUser) { Write-Err 'SSH user must not be empty'; return $false }
    if ($SrvPort -notmatch '^\d+$') { Write-Err 'SSH port must be a number'; return $false }
    return $true
}

function Invoke-ItemConfig {     # item 4: base Host block - form: edit fields, then apply
    Initialize-SshDir
    $srvHost = ''; $srvUser = ''; $srvPort = ''; $useProxy = $false; $revInt = 0
    # Pre-fill from the existing block; RemoteForward/ProxyCommand pass through untouched
    if (Test-StatusConfig) {
        $srvHost = Get-ConfigBlockValue 'HostName'
        $srvUser = Get-ConfigBlockValue 'User'
        $srvPort = Get-ConfigBlockValue 'Port'
        $useProxy = Test-ConfigProxyOn
        $rev = Get-ConfigBlockRport
        if ($rev) { $revInt = [int]$rev }
    }
    if ($ServerHost) { $srvHost = $ServerHost }
    if ($ServerUser) { $srvUser = $ServerUser }
    if ($ServerPort -gt 0) { $srvPort = "$ServerPort" }
    if (-not $srvPort) { $srvPort = '22' }

    if ($ServerHost -and $ServerUser) {
        # Non-interactive (documented parameter overrides): no form, write immediately
        if (-not (Test-ConfigFields $srvHost $srvUser $srvPort)) { throw 'Invalid tunnel config values' }
        Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort ([int]$srvPort) -RevPort $revInt -UseProxy:$useProxy
        return
    }

    while ($true) {
        Write-Host ''
        Write-Host "SSH config shortcut (Host $TunnelAlias) - edit fields, then apply:"
        $hostShow = if ($srvHost) { $srvHost } else { '(not set)' }
        $userShow = if ($srvUser) { $srvUser } else { '(not set)' }
        Write-Host ('  1) {0,-22} {1}' -f 'Server host / IP', $hostShow)
        Write-Host ('  2) {0,-22} {1}' -f 'SSH user', $userShow)
        Write-Host ('  3) {0,-22} {1}' -f 'SSH port', $srvPort)
        Write-Host '  a) Apply & write config'
        Write-Host '  q) Cancel (no changes)'
        $sel = (Read-Default 'Select [1-3, a, q]').Trim()
        switch -Regex ($sel) {
            '^1$' { $srvHost = Read-Default 'Server host / IP' $srvHost }
            '^2$' { $srvUser = Read-Default 'SSH user' $srvUser }
            '^3$' { $srvPort = Read-Default 'SSH port' $srvPort }
            '^[Aa]$' {
                if (Test-ConfigFields $srvHost $srvUser $srvPort) {
                    Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort ([int]$srvPort) -RevPort $revInt -UseProxy:$useProxy -Force
                    return
                }
            }
            '^[Qq]$' { Write-Info 'Cancelled - nothing changed'; return }
            default { Write-Warn "Unknown selection: $sel" }
        }
    }
}

function Invoke-ItemRport {   # item 5: reverse tunnel port (RemoteForward)
    if (-not (Test-StatusConfig)) { throw "No Host $TunnelAlias block yet - run item 4 first" }
    $srvHost = Get-ConfigBlockValue 'HostName'
    $srvUser = Get-ConfigBlockValue 'User'
    $srvPort = Get-ConfigBlockValue 'Port'
    if (-not ($srvHost -and $srvUser -and $srvPort)) { throw "Could not read the Host $TunnelAlias block - re-run item 4" }
    $useProxy = Test-ConfigProxyOn
    $cur = Get-ConfigBlockRport
    $revPort = if ($ReversePort -gt 0) { "$ReversePort" } else {
        Read-Default 'Reverse SSH port on the server (used by Claude/Codex to connect back)' $(if ($cur) { $cur } else { '2222' })
    }
    if ($revPort -notmatch '^\d+$') { throw 'Reverse port must be a number' }
    Write-SshConfigBlock -SrvHost $srvHost -SrvUser $srvUser -SrvPort ([int]$srvPort) -RevPort ([int]$revPort) -UseProxy:$useProxy -Force
    Write-Info "Reverse port $revPort set - the tunnel rides on your ssh $TunnelAlias connection."
}

function Invoke-ItemShowKey {    # item 8: print the local public key
    if (-not (Test-Path "$KeyPath.pub")) {
        if (-not (Test-Path $KeyPath)) {
            if (-not (Read-YesNo 'No local key yet - generate it now' $true)) { throw 'No key to show' }
        }
        Invoke-ItemKey
    }
    Write-Host ''
    Write-Info "Local public key - paste it into server/setup-server.sh (item 2) on the server; that authorizes the tunnel login (ssh $TunnelAlias):"
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
function Test-StatusXray { return (Test-Path $XrayLauncher) -and (Test-Path $XrayRelay) -and [bool](Resolve-XrayExe) }
function Test-ConfigProxyOn {
    $raw = Get-Content -Raw $SshConfig -ErrorAction SilentlyContinue
    return [bool]($raw -and $raw.Contains('ProxyCommand') -and ($raw.Contains($XrayRelay) -or $raw.Contains($XrayLauncher)))
}

# ---------------------------------------------------------------- menu
function Format-Mark { param([bool]$Ok) if ($Ok) { '[done]' } else { '[ -  ]' } }

function Show-Menu {
    Write-Host ''
    Write-Host '----------------------------------------------------------'
    Write-Host ('  1) {0,-50} {1}' -f 'Incoming SSH - OpenSSH Server + harden  [admin]', (Format-Mark (Test-StatusSshd)))
    Write-Host ('  2) {0,-50} {1}' -f 'Local SSH key (~\.ssh\id_ed25519)', (Format-Mark (Test-StatusKey)))
    Write-Host ('  3) {0,-50} {1}' -f "Authorize the server's connect-back key", (Format-Mark (Test-StatusAuthorize)))
    Write-Host ('  4) {0,-50} {1}' -f 'SSH config shortcut (Host remote-claude)', (Format-Mark (Test-StatusConfig)))
    Write-Host ('  5) {0,-50} {1}' -f 'Reverse tunnel port (RemoteForward)', (Format-Mark (Test-StatusRport)))
    Write-Host ('  6) {0,-50} {1}' -f 'xray client (binary + launcher)', (Format-Mark (Test-StatusXray)))
    Write-Host ('  7) {0,-50} {1}' -f 'Route tunnel through xray (ProxyCommand)', (Format-Mark (Test-ConfigProxyOn)))
    Write-Host  '  8) Show local public key (paste into server setup)'
    Write-Host  '  q) Quit'
}

if (-not $env:RC_SOURCED_FOR_TEST) {
    :menu while ($true) {
        Show-Menu
        $choice = (Read-Host 'Select [1-8, q]').Trim()
        if ($choice -match '^[Qq]$') { break menu }
        $fn = switch ($choice) {
            '1' { 'Invoke-ItemSshd' }
            '2' { 'Invoke-ItemKey' }
            '3' { 'Invoke-ItemAuthorize' }
            '4' { 'Invoke-ItemConfig' }
            '5' { 'Invoke-ItemRport' }
            '6' { 'Invoke-ItemXray' }
            '7' { 'Invoke-ItemProxy' }
            '8' { 'Invoke-ItemShowKey' }
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
    Write-Info "Connect as usual - VSCode Remote-SSH (host $TunnelAlias) or: ssh $TunnelAlias"
    Write-Info 'The reverse tunnel rides on that connection (one connection at a time).'
    Write-Info "Then on the server: ssh my-device 'echo ok' should print ok"
}
