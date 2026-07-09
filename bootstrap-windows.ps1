<#
.SYNOPSIS
  Reverse SSH dev-environment bootstrap for Windows.

.DESCRIPTION
  Prepares this Windows machine so a remote server's Claude / Codex agent can
  SSH back into it through a reverse tunnel:

    local PC  -- ssh -N claude-dev-tunnel -->  remote server
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
    6. Writes a managed "Host claude-dev-tunnel" block into
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

$TunnelAlias  = 'claude-dev-tunnel'
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
$KeepAlivePs1 = Join-Path $SshDir 'claude-dev-tunnel-keepalive.ps1'
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
if (-not $ServerHost)  { $ServerHost  = Read-Default '远程服务器 hostname / IP' }
if (-not $ServerHost)  { Write-Err '服务器地址不能为空'; exit 1 }
if (-not $ServerUser)  { $ServerUser  = Read-Default '远程服务器 SSH 用户名' }
if (-not $ServerUser)  { Write-Err '服务器用户名不能为空'; exit 1 }
if ($ServerPort -le 0) { $ServerPort  = [int](Read-Default '远程服务器 SSH 端口' '22') }
if ($ReversePort -le 0){ $ReversePort = [int](Read-Default '服务器上反向 SSH 端口 (Claude/Codex 连回本机用)' '2222') }
if (-not $LocalUser)   { $LocalUser   = Read-Default '本地用户名 (服务器侧连回来时使用)' $env:USERNAME }

Write-Host ''
Write-Host '服务器侧 public key：即服务器上 Claude / Codex 用来反连本机的那把 key 的 .pub 内容'
Write-Host "(整行粘贴，例如 'ssh-ed25519 AAAA... comment'；留空则跳过这一步)"
if (-not $ServerPublicKey) { $ServerPublicKey = Read-Default '服务器侧 public key' '' }

$DisablePassword = Read-YesNo '禁用本机 sshd 密码登录 (推荐，仅允许 public key)' $true
$LoopbackOnly    = Read-YesNo '让本机 sshd 只监听 127.0.0.1 (推荐；注意：局域网将无法直接 SSH 到本机)' $true

# ---------------------------------------------------------------- OpenSSH Server install
Write-Info '检查 OpenSSH Server 是否已安装'
$cap = Get-WindowsCapability -Online | Where-Object Name -like 'OpenSSH.Server*' | Select-Object -First 1
if (-not $cap) {
    Write-Err '未找到 OpenSSH.Server capability，请确认 Windows 10 1809+ / Windows 11。'
    exit 1
}
if ($cap.State -ne 'Installed') {
    Write-Info '安装 OpenSSH Server（可能需要几分钟）...'
    Add-WindowsCapability -Online -Name $cap.Name | Out-Null
    Write-Info 'OpenSSH Server 安装完成'
} else {
    Write-Info 'OpenSSH Server 已安装'
}
$clientCap = Get-WindowsCapability -Online | Where-Object Name -like 'OpenSSH.Client*' | Select-Object -First 1
if ($clientCap -and $clientCap.State -ne 'Installed') {
    Write-Info '安装 OpenSSH Client（提供 ssh / ssh-keygen）...'
    Add-WindowsCapability -Online -Name $clientCap.Name | Out-Null
}

# ---------------------------------------------------------------- start sshd + auto start
# 先启动一次，让 OpenSSH 在 %ProgramData%\ssh 下生成默认 sshd_config 和主机密钥
Write-Info '启动 sshd 服务并设置开机自启'
Set-Service -Name sshd -StartupType Automatic
Start-Service -Name sshd

# ---------------------------------------------------------------- sshd_config
if (-not (Test-Path $SshdConfig)) {
    Write-Err "未找到 $SshdConfig（sshd 首次启动后应自动生成）"
    exit 1
}
$backupPath = "$SshdConfig.claude-bak-$Ts"
Copy-Item $SshdConfig $backupPath
Write-Info "已备份 sshd_config -> $backupPath"

$raw = Get-Content -Raw $SshdConfig

# 1) 注释掉 administrators_authorized_keys 的 Match 块，让管理员用户
#    也使用自己的 %USERPROFILE%\.ssh\authorized_keys。
#    已被注释过的行不再匹配该模式，因此重复运行是幂等的。
$raw = $raw -replace '(?m)^([ \t]*Match[ \t]+Group[ \t]+administrators[ \t]*)\r?$', '# claude-bootstrap disabled: $1'
$raw = $raw -replace '(?m)^([ \t]*AuthorizedKeysFile[ \t]+__PROGRAMDATA__[/\\]ssh[/\\]administrators_authorized_keys[ \t]*)\r?$', '# claude-bootstrap disabled: $1'

# 2) 设置全局指令（只替换第一处匹配；找不到就追加到文件末尾）
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

Write-Info '校验 sshd 配置 (sshd -t)'
# sshd -t 的报错走 stderr；在 EAP=Stop 下 2>&1 会把 stderr 包装成异常
# (PS 5.1 NativeCommandError)，所以校验期间临时放宽，确保失败时能恢复备份
$prevEap = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$sshdCheck = & $SshdExe -t 2>&1
$sshdCheckCode = $LASTEXITCODE
$ErrorActionPreference = $prevEap
$sshdCheck | ForEach-Object { Write-Host "    $_" }
if ($sshdCheckCode -ne 0) {
    Write-Err 'sshd 配置校验失败，恢复备份'
    Copy-Item $backupPath $SshdConfig -Force
    exit 1
}
Write-Info '配置校验通过，重启 sshd'
Restart-Service -Name sshd

# ---------------------------------------------------------------- ~/.ssh + ACL
Write-Info '准备 %USERPROFILE%\.ssh 目录并收紧 ACL'
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
            Write-Err '粘贴的内容不是合法的 SSH public key，请检查后重新运行'
            exit 1
        }
    } finally {
        Remove-Item $tmp -ErrorAction SilentlyContinue
    }
    $blob = ($ServerPublicKey -split '\s+' | Where-Object { $_ -like 'AAAA*' } | Select-Object -First 1)
    if (-not $blob) { Write-Err '无法从 public key 中解析 key 数据'; exit 1 }
    $existing = Get-Content -Raw $AuthKeys -ErrorAction SilentlyContinue
    if ($existing -and $existing.Contains($blob)) {
        Write-Info '该 public key 已在 authorized_keys 中，跳过'
    } else {
        $entry = "from=`"127.0.0.1,::1`",no-agent-forwarding,no-X11-forwarding $ServerPublicKey"
        Add-Content -Path $AuthKeys -Value $entry -Encoding ascii
        Write-Info '已写入 authorized_keys（限制为仅可从 loopback 登录）'
    }
} else {
    Write-Warn "未提供服务器侧 public key。之后可将其追加到 $AuthKeys，"
    Write-Warn '建议格式: from="127.0.0.1,::1",no-agent-forwarding,no-X11-forwarding <public-key>'
}

# ---------------------------------------------------------------- local tunnel key
if ((Test-Path $KeyPath) -and (Test-Path "$KeyPath.pub")) {
    Write-Info "本地隧道 key 已存在: $KeyPath"
} else {
    Write-Info "生成本地连接服务器用的 SSH key: $KeyPath"
    & $SshKeygenExe -t ed25519 -f $KeyPath -N '""' -C 'claude-tunnel' | Out-Null
    if ($LASTEXITCODE -ne 0) { Write-Err 'ssh-keygen 失败'; exit 1 }
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
    if (Read-YesNo "~\.ssh\config 中已有 $TunnelAlias 配置块，是否更新" $true) {
        Copy-Item $SshConfig "$SshConfig.claude-bak-$Ts"
        Write-Info "已备份 ssh config -> $SshConfig.claude-bak-$Ts"
        $escBegin = [regex]::Escape($BeginMark)
        $escEnd   = [regex]::Escape($EndMark)
        $configRaw = [regex]::Replace($configRaw, "(?s)$escBegin.*?$escEnd(\r?\n)?", '')
    } else {
        Write-Warn '保留现有配置块，跳过写入'
        $writeBlock = $false
    }
} else {
    if ($configRaw -match "(?m)^\s*Host\s+.*\b$TunnelAlias\b") {
        Write-Warn "~\.ssh\config 中存在非本工具管理的 'Host $TunnelAlias' 配置块。"
        Write-Warn 'ssh 采用先到先得，靠前的旧配置会覆盖本工具写入的内容。'
        if (-not (Read-YesNo '仍然继续写入 (建议先手动清理旧配置块)' $false)) {
            Write-Err "已中止。请手动移除旧的 Host $TunnelAlias 块后重新运行"
            exit 1
        }
    }
    Copy-Item $SshConfig "$SshConfig.claude-bak-$Ts"
    Write-Info "已备份 ssh config -> $SshConfig.claude-bak-$Ts"
}

if ($writeBlock) {
    $configRaw = $configRaw.TrimEnd()
    if ($configRaw) { $configRaw += "`r`n`r`n" }
    $configRaw += $configBlock.Replace("`r`n", "`n").Replace("`n", "`r`n") + "`r`n"
    [System.IO.File]::WriteAllText($SshConfig, $configRaw)
    Write-Info "已写入 Host $TunnelAlias 到 $SshConfig"
}
Set-StrictAcl -Path $SshConfig

# ---------------------------------------------------------------- copy key to server (optional)
Write-Host ''
Write-Info "本地隧道 public key（需要加入服务器上 $ServerUser 的 ~/.ssh/authorized_keys）："
Write-Host ''
Get-Content "$KeyPath.pub" | Write-Host
Write-Host ''
if (Read-YesNo '现在自动上传到服务器 (相当于 ssh-copy-id，需要输入服务器密码或已有可用认证)' $false) {
    $pub = (Get-Content "$KeyPath.pub" -Raw).Trim()
    $remoteCmd = "mkdir -p ~/.ssh && chmod 700 ~/.ssh && grep -qF '$pub' ~/.ssh/authorized_keys 2>/dev/null || echo '$pub' >> ~/.ssh/authorized_keys; chmod 600 ~/.ssh/authorized_keys"
    & $SshExe -p $ServerPort "$ServerUser@$ServerHost" $remoteCmd
    if ($LASTEXITCODE -eq 0) { Write-Info '已上传' }
    else { Write-Warn '上传失败，请手动将上述 public key 追加到服务器的 ~/.ssh/authorized_keys' }
}

# ---------------------------------------------------------------- Scheduled Task (optional)
Write-Host ''
$autostart = Read-YesNo '注册计划任务，登录后自动拉起并保持 tunnel (可选)' $false
if ($autostart) {
    # keepalive 脚本：ssh 断开后等待 15 秒自动重连
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
        -Settings $settings -Description 'Keep the claude-dev-tunnel reverse SSH tunnel alive' -Force | Out-Null
    Write-Info "计划任务已注册: $TaskName（下次登录自动启动）"
    if (Read-YesNo '现在就启动计划任务' $true) {
        Start-ScheduledTask -TaskName $TaskName
        Write-Info '计划任务已启动'
    }
}

# ---------------------------------------------------------------- summary
Write-Host @"

==========================================================
 完成！后续步骤
==========================================================
1. 确认本地隧道 public key 已加入服务器 (~$ServerUser/.ssh/authorized_keys)：
     $KeyPath.pub

2. 手动启动 tunnel（前台保持运行）：
     ssh -N $TunnelAlias

3. 只要 tunnel 保持连接，在远程服务器上 Claude / Codex 即可用：
     ssh -i ~/.ssh/claude_to_local_ed25519 -p $ReversePort $LocalUser@127.0.0.1
   （-i 指向服务器上那把反连 key 的私钥路径，按实际情况调整）
"@
if ($autostart) {
    Write-Host @"
tunnel 已配置为登录自启（计划任务 $TaskName）。停止方式：
     Stop-ScheduledTask -TaskName $TaskName
     Unregister-ScheduledTask -TaskName $TaskName -Confirm:`$false
"@
} else {
    Write-Host '如需登录自启，重新运行脚本并在计划任务步骤选择 yes。'
}
Write-Host ''
Write-Host '回滚方法见 README.md 的 “删除 / 回滚” 一节。'
