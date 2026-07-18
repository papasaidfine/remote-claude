$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$bootstrap = Join-Path (Join-Path $here '..') (Join-Path 'local' 'bootstrap-windows.ps1')

$script:fail = 0
function Check([string]$Desc, [bool]$Cond) {
    if ($Cond) { Write-Host "ok   - $Desc" } else { Write-Host "FAIL - $Desc"; $script:fail = 1 }
}

# Whole-file syntax parse
$parseErrors = $null
[System.Management.Automation.Language.Parser]::ParseFile($bootstrap, [ref]$null, [ref]$parseErrors) | Out-Null
Check 'bootstrap parses without errors' ($parseErrors.Count -eq 0)

# Prime the JSON cmdlets before SystemRoot gets sandboxed below: they lazily
# load System.Web.Extensions assemblies (e.g. JavaScriptSerializer for
# ConvertTo-Json -Depth), which read the system.web/httpRuntime config
# section. On some hosts, first use after SystemRoot points outside the real
# Windows install throws a ConfigurationErrorsException / FileLoadException
# resolving that section's GAC assembly.
[ordered]@{ warm = 1 } | ConvertTo-Json -Depth 10 | ConvertFrom-Json | Out-Null

# Sandbox: every profile path the script derives must land in a temp dir
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("rcwin-" + [System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
$env:RC_SOURCED_FOR_TEST = '1'
$env:USERPROFILE  = Join-Path $tmp 'home'
$env:LOCALAPPDATA = Join-Path $tmp 'lad'
$env:ProgramData  = Join-Path $tmp 'pd'
$env:SystemRoot   = Join-Path $tmp 'sr'
$env:TEMP         = Join-Path $tmp 'tmp'
New-Item -ItemType Directory -Path $env:USERPROFILE, $env:LOCALAPPDATA, $env:ProgramData, $env:SystemRoot, $env:TEMP | Out-Null

. $bootstrap

# icacls does not exist off-Windows; the ACL step is irrelevant to these tests
function Set-StrictAcl { param([string]$Path, [switch]$Directory) }

Check 'sourcing defines Invoke-ItemConfig' ([bool](Get-Command Invoke-ItemConfig -ErrorAction SilentlyContinue))
Check 'paths are sandboxed' ($SshConfig.StartsWith($tmp))

# --- Read-VlessNodes ----------------------------------------------------------
New-Item -ItemType Directory -Force -Path $RcConfigDir | Out-Null
@"

# comment line
vless://uuid-a@a.example:443?type=tcp#node-a
   # indented comment
vless://uuid-b@b.example:443?type=tcp#node-b
"@ | Set-Content $VlessNodes
$nodes = Read-VlessNodes $VlessNodes
Check 'nodes: two nodes survive filtering' ($nodes.Count -eq 2)
Check 'nodes: first node kept'  ($nodes[0] -eq 'vless://uuid-a@a.example:443?type=tcp#node-a')
Check 'nodes: second node kept' ($nodes[1] -eq 'vless://uuid-b@b.example:443?type=tcp#node-b')
Check 'nodes: missing file gives empty set' ((Read-VlessNodes (Join-Path $RcConfigDir 'nope.txt')).Count -eq 0)

# --- ConvertTo-VlessJson ------------------------------------------------------
$u = 'vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp&security=reality&pbk=PUBKEYXYZ&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#node'
$tpl = ConvertTo-VlessJson $u
$j = $tpl | ConvertFrom-Json
$vnext = $j.outbounds[0].settings.vnext[0]
Check 'reality: uuid'        ($vnext.users[0].id -eq '11111111-2222-3333-4444-555555555555')
Check 'reality: flow'        ($vnext.users[0].flow -eq 'xtls-rprx-vision')
Check 'reality: address'     ($vnext.address -eq 'example.com')
Check 'reality: server port' ($vnext.port -eq 443)
$ss = $j.outbounds[0].streamSettings
Check 'reality: security'    ($ss.security -eq 'reality')
Check 'reality: pbk'         ($ss.realitySettings.publicKey -eq 'PUBKEYXYZ')
Check 'reality: sid'         ($ss.realitySettings.shortId -eq 'ab12')
Check 'reality: sni'         ($ss.realitySettings.serverName -eq 'www.microsoft.com')
Check 'reality: fp'          ($ss.realitySettings.fingerprint -eq 'chrome')
$inb = $j.inbounds[0]
Check 'inbound: dokodemo'          ($inb.protocol -eq 'dokodemo-door')
Check 'inbound: port placeholder'  ($inb.port -eq '__DOKO_PORT__')
Check 'inbound: dest placeholders' ($inb.settings.address -eq '__DEST_HOST__' -and $inb.settings.port -eq '__DEST_PORT__')
Check 'log placeholder' ($j.log.error -eq '__LOG_FILE__')

# The launcher's materialization contract: quoted numeric placeholders become numbers
$mat = $tpl.Replace('"__DOKO_PORT__"', '12345').Replace('"__DEST_PORT__"', '22').Replace('__DEST_HOST__', '203.0.113.7').Replace('__LOG_FILE__', 'C:/t/x.log')
$mj = $mat | ConvertFrom-Json
Check 'materialized: numeric ports' ($mj.inbounds[0].port -eq 12345 -and $mj.inbounds[0].settings.port -eq 22)
Check 'materialized: dest host'     ($mj.inbounds[0].settings.address -eq '203.0.113.7')

# TLS + ws (percent-encoded path)
$u2 = 'vless://aaaa@host.tld:8443?type=ws&security=tls&sni=host.tld&path=%2Fws&host=host.tld'
$j2 = (ConvertTo-VlessJson $u2) | ConvertFrom-Json
$ss2 = $j2.outbounds[0].streamSettings
Check 'tls-ws: security' ($ss2.security -eq 'tls')
Check 'tls-ws: sni'      ($ss2.tlsSettings.serverName -eq 'host.tld')
Check 'tls-ws: ws path'  ($ss2.wsSettings.path -eq '/ws')
Check 'tls-ws: ws host'  ($ss2.wsSettings.headers.Host -eq 'host.tld')

# Unsupported values must throw
$threw = $false
try { ConvertTo-VlessJson 'vless://x@h:1?security=weird&type=tcp' | Out-Null } catch { $threw = $true }
Check 'unsupported security throws' $threw
$threw = $false
try { ConvertTo-VlessJson 'https://not-vless' | Out-Null } catch { $threw = $true }
Check 'non-vless url throws' $threw

# --- Write-SshConfigBlock / Get-ConfigBlockValue ------------------------------
New-Item -ItemType Directory -Force -Path $SshDir | Out-Null
[System.IO.File]::WriteAllText($SshConfig, "Host other`r`n    User bob`r`n")
Write-SshConfigBlock -SrvHost '203.0.113.7' -SrvUser 'ubuntu' -SrvPort 22 -RevPort 2222

Check 'parse: HostName'      ((Get-ConfigBlockValue 'HostName') -eq '203.0.113.7')
Check 'parse: User'          ((Get-ConfigBlockValue 'User') -eq 'ubuntu')
Check 'parse: Port'          ((Get-ConfigBlockValue 'Port') -eq '22')
Check 'parse: RemoteForward' ((Get-ConfigBlockValue 'RemoteForward') -eq '127.0.0.1:2222')
Check 'parse: missing key'   ((Get-ConfigBlockValue 'ProxyCommand') -eq '')

# Any prompt from here on is a bug: -Force must never ask
function Read-YesNo { throw 'unexpected Read-YesNo prompt' }
Write-SshConfigBlock -SrvHost '198.51.100.9' -SrvUser 'carol' -SrvPort 2200 -RevPort 2345 -Force
$raw = Get-Content -Raw $SshConfig
Check 'force: rewritten without prompting' ($raw.Contains('HostName 198.51.100.9'))
Check 'force: reverse port updated' ($raw.Contains('RemoteForward 127.0.0.1:2345 127.0.0.1:22'))
Check 'force: unmanaged content kept' ($raw.Contains('Host other'))
Check 'force: single managed block' (([regex]::Matches($raw, [regex]::Escape($BeginMark))).Count -eq 1)

# -UseProxy injects the launcher ProxyCommand line
Write-SshConfigBlock -SrvHost '203.0.113.7' -SrvUser 'ubuntu' -SrvPort 22 -RevPort 2222 -UseProxy -Force
$raw = Get-Content -Raw $SshConfig
Check 'proxy: ProxyCommand line present' ($raw.Contains("-File `"$XrayLauncher`" %h %p"))
Check 'proxy: after IdentitiesOnly' ($raw.IndexOf('IdentitiesOnly yes') -lt $raw.IndexOf('ProxyCommand'))

# --- optional reverse port ------------------------------------------------------
Write-SshConfigBlock -SrvHost '203.0.113.7' -SrvUser 'ubuntu' -SrvPort 22 -RevPort 0 -Force
$raw = Get-Content -Raw $SshConfig
Check 'no-rport: RemoteForward omitted' (-not $raw.Contains('RemoteForward'))
Check 'no-rport: base fields written'   ($raw.Contains('HostName 203.0.113.7'))
Check 'no-rport: helper empty'          ((Get-ConfigBlockRport) -eq '')
Check 'no-rport: status false'          (-not (Test-StatusRport))
Write-SshConfigBlock -SrvHost '203.0.113.7' -SrvUser 'ubuntu' -SrvPort 22 -RevPort 2222 -Force
Check 'rport: helper reads port' ((Get-ConfigBlockRport) -eq '2222')
Check 'rport: status true'       (Test-StatusRport)

# --- Write-XrayLauncher -------------------------------------------------------
Write-XrayLauncher
Check 'launcher written' (Test-Path $XrayLauncher)
$launchErrors = $null
[System.Management.Automation.Language.Parser]::ParseFile($XrayLauncher, [ref]$null, [ref]$launchErrors) | Out-Null
Check 'launcher parses without errors' ($launchErrors.Count -eq 0)
$launcherSrc = Get-Content -Raw $XrayLauncher
Check 'launcher: kill-on-close job'     ($launcherSrc.Contains('0x2000'))
Check 'launcher: replaces placeholders' ($launcherSrc.Contains('"__DOKO_PORT__"') -and $launcherSrc.Contains('__DEST_HOST__'))
Check 'launcher embeds ConvertTo-VlessJson' ($launcherSrc.Contains('function ConvertTo-VlessJson'))
Check 'launcher embeds Read-VlessNodes'     ($launcherSrc.Contains('function Read-VlessNodes'))
Check 'launcher picks a random node'        ($launcherSrc.Contains('Get-Random'))
Check 'launcher reads vless-nodes.txt'      ($launcherSrc.Contains('vless-nodes.txt'))
Check 'launcher no longer reads xray.json'  (-not $launcherSrc.Contains("'xray.json'"))
Check 'launcher zero-nodes msg updated'     ($launcherSrc.Contains('edit the file and add one'))

# --- Test-StatusXray: launcher + binary only --------------------------------------
Remove-Item $VlessNodes -Force -ErrorAction SilentlyContinue
Check 'status: not ready without binary' (-not (Test-StatusXray))
New-Item -ItemType Directory -Force -Path (Split-Path $XrayVendorBin) | Out-Null
New-Item -ItemType File -Force -Path $XrayVendorBin | Out-Null
Check 'status: ready with launcher + binary (no nodes needed)' (Test-StatusXray)

# --- Download proxy plumbing --------------------------------------------------
$script:DlProxy = 'http://127.0.0.1:7890'
$pa = Get-ProxyArgs
Check 'proxy: Get-ProxyArgs carries the proxy' ($pa.Proxy -eq 'http://127.0.0.1:7890')
$script:DlProxy = ''
Check 'proxy: Get-ProxyArgs empty when direct' ((Get-ProxyArgs).Count -eq 0)

function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [string]$OutFile, [int]$TimeoutSec, [string]$Proxy, [string]$Uri)
    $script:iwrCall = $PSBoundParameters
}
function Expand-Archive {
    param([string]$Path, [string]$DestinationPath, [switch]$Force)
    New-Item -ItemType Directory -Force -Path $DestinationPath | Out-Null
    Set-Content -Path $XrayVendorBin -Value 'stub'
}
$script:DlProxy = 'http://127.0.0.1:7890'
Install-XrayRelease
Check 'iwr: -Proxy passed when a proxy is set' ($script:iwrCall['Proxy'] -eq 'http://127.0.0.1:7890')
Check 'iwr: -TimeoutSec 30 on the zip download' ($script:iwrCall['TimeoutSec'] -eq 30)
$script:DlProxy = ''
$script:iwrCall = $null
Install-XrayRelease
Check 'iwr: no -Proxy when direct' (-not $script:iwrCall.ContainsKey('Proxy'))

function Invoke-RestMethod {
    param([switch]$UseBasicParsing, [int]$TimeoutSec, [string]$Proxy, [string]$Uri)
    $script:irmCall = $PSBoundParameters
    return [pscustomobject]@{ tag_name = 'v25.1.0' }
}
$script:DlProxy = 'http://127.0.0.1:7890'
Check 'irm: version parsed through the mock'   ((Get-XrayLatestVersion) -eq '25.1.0')
Check 'irm: -Proxy passed when a proxy is set' ($script:irmCall['Proxy'] -eq 'http://127.0.0.1:7890')
$script:DlProxy = ''
$script:irmCall = $null
Get-XrayLatestVersion | Out-Null
Check 'irm: no -Proxy when direct' (-not $script:irmCall.ContainsKey('Proxy'))

# --- Invoke-ItemXray: update flow via overrides -----------------------------------
$script:fakeLatest = ''
$script:downloaded = $false
function Get-XrayLocalVersion { param([string]$Exe) return '25.0.0' }
function Get-XrayLatestVersion { return $script:fakeLatest }
function Install-XrayRelease { $script:downloaded = $true }
function Read-DlProxy { $script:DlProxy = '' }

New-Item -ItemType File -Force -Path $XrayJson | Out-Null
$script:fakeLatest = '25.0.0'
Invoke-ItemXray
Check 'item6: up-to-date -> no download' (-not $script:downloaded)
Check 'item6: stale xray.json removed'   (-not (Test-Path $XrayJson))
Check 'item6: nodes template created'    (Test-Path $VlessNodes)
Check 'item6: template has no nodes'     ((Read-VlessNodes $VlessNodes).Count -eq 0)

$script:fakeLatest = '26.0.0'
Invoke-ItemXray
Check 'item6: newer release -> download' $script:downloaded

$script:downloaded = $false
$script:fakeLatest = ''
Invoke-ItemXray
Check 'item6: unreachable API -> no download' (-not $script:downloaded)

# VLESS_URL seeds the first node when the file is created
Remove-Item $VlessNodes -Force
$VlessUrl = 'vless://seed@s.example:443?type=tcp#seeded'
$script:fakeLatest = '25.0.0'
Invoke-ItemXray
Check 'item6: VlessUrl seeded' ((Get-Content -Raw $VlessNodes).Contains('vless://seed@s.example:443?type=tcp#seeded'))
$VlessUrl = ''

# An existing nodes file is left alone
Set-Content $VlessNodes 'vless://keep@k.example:443?type=tcp#keep'
Invoke-ItemXray
Check 'item6: existing nodes file untouched' ((Get-Content -Raw $VlessNodes).Contains('vless://keep@k.example'))

# --- Invoke-ItemProxy (menu item 7) -------------------------------------------
# Reset to a known block without the proxy (Read-YesNo still throws on any prompt)
Write-SshConfigBlock -SrvHost '203.0.113.7' -SrvUser 'ubuntu' -SrvPort 22 -RevPort 2222 -Force

# No managed block -> error
$savedConfig = $SshConfig
$SshConfig = Join-Path $tmp 'no-such-config'
$threw = $false
try { Invoke-ItemProxy } catch { $threw = $true }
Check 'toggle: no managed block errors' $threw
$SshConfig = $savedConfig

# xray artifacts exist from the launcher tests, so enabling must work, promptless
Invoke-ItemProxy
$raw = Get-Content -Raw $SshConfig
Check 'toggle on: ProxyCommand added'     ($raw.Contains("-File `"$XrayLauncher`" %h %p"))
Check 'toggle on: HostName preserved'     ($raw.Contains('HostName 203.0.113.7'))
Check 'toggle on: User preserved'         ($raw.Contains('User ubuntu'))
Check 'toggle on: reverse port preserved' ($raw.Contains('RemoteForward 127.0.0.1:2222 127.0.0.1:22'))
Check 'toggle on: unmanaged content kept' ($raw.Contains('Host other'))
Check 'toggle on: proxy status detected'  (Test-ConfigProxyOn)

# Toggle OFF
Invoke-ItemProxy
$raw = Get-Content -Raw $SshConfig
Check 'toggle off: ProxyCommand removed' (-not $raw.Contains('ProxyCommand'))
Check 'toggle off: HostName preserved'   ($raw.Contains('HostName 203.0.113.7'))

# Toggle ON again - nothing duplicated
Invoke-ItemProxy
$raw = Get-Content -Raw $SshConfig
Check 're-toggle: exactly one ProxyCommand'  (([regex]::Matches($raw, 'ProxyCommand')).Count -eq 1)
Check 're-toggle: exactly one managed block' (([regex]::Matches($raw, [regex]::Escape($BeginMark))).Count -eq 1)

# Enabling with an empty nodes file must error
Invoke-ItemProxy   # back to OFF
Set-Content $VlessNodes '# comments only'
$threw = $false
try { Invoke-ItemProxy } catch { $threw = $true }
Check 'proxy: enabling without nodes errors' $threw
Set-Content $VlessNodes 'vless://keep@k.example:443?type=tcp#keep'

# Independence: proxy works on a block WITHOUT a reverse port
Write-SshConfigBlock -SrvHost '203.0.113.7' -SrvUser 'ubuntu' -SrvPort 22 -RevPort 0 -Force
Invoke-ItemProxy
$raw = Get-Content -Raw $SshConfig
Check 'proxy: on rportless block'     ($raw.Contains('ProxyCommand'))
Check 'proxy: still no RemoteForward' (-not $raw.Contains('RemoteForward'))
Invoke-ItemProxy   # OFF again
Write-SshConfigBlock -SrvHost '203.0.113.7' -SrvUser 'ubuntu' -SrvPort 22 -RevPort 2222 -Force

# Enabling without xray must error and leave the config untouched (proxy is OFF here)
Remove-Item $XrayLauncher -Force
$threw = $false
try { Invoke-ItemProxy } catch { $threw = $true }
Check 'toggle: enabling without xray errors' $threw
Check 'toggle: config untouched without xray' (-not (Get-Content -Raw $SshConfig).Contains('ProxyCommand'))

# --- Invoke-ItemConfig form ----------------------------------------------------
# Scripted input: each prompt pops one queued value; '' means "keep the default";
# a prompt with an empty queue throws, proving exactly N inputs are consumed.
$script:inputQueue = New-Object System.Collections.Queue
function Read-Default {
    param([string]$Prompt, [string]$Default = '')
    if ($script:inputQueue.Count -eq 0) { throw "input queue empty at: $Prompt" }
    $v = [string]$script:inputQueue.Dequeue()
    if ($v -eq '') { return $Default }
    return $v
}

# Known starting block: host/user/port + RemoteForward, no proxy
Write-SshConfigBlock -SrvHost '203.0.113.7' -SrvUser 'ubuntu' -SrvPort 22 -RevPort 2222 -Force

# Pre-filled 3-field form; edit the host; RemoteForward + proxy state pass through
'1', '198.51.100.77', 'a' | ForEach-Object { $script:inputQueue.Enqueue($_) }
Invoke-ItemConfig
$raw = Get-Content -Raw $SshConfig
Check 'form: edited host written'       ($raw.Contains('HostName 198.51.100.77'))
Check 'form: untouched user preserved'  ($raw.Contains('User ubuntu'))
Check 'form: RemoteForward preserved'   ($raw.Contains('RemoteForward 127.0.0.1:2222 127.0.0.1:22'))
Check 'form: single managed block'      (([regex]::Matches($raw, [regex]::Escape($BeginMark))).Count -eq 1)

# Cancel changes nothing
'1', '203.0.113.99', 'q' | ForEach-Object { $script:inputQueue.Enqueue($_) }
Invoke-ItemConfig
Check 'form: cancel leaves config untouched' (-not (Get-Content -Raw $SshConfig).Contains('203.0.113.99'))

# Validation: bad ssh port rejected, then fixed
'3', 'abc', 'a', '3', '2200', 'a' | ForEach-Object { $script:inputQueue.Enqueue($_) }
Invoke-ItemConfig
Check 'form: bad port rejected then fixed' ((Get-Content -Raw $SshConfig).Contains('Port 2200'))
Check 'form: queue fully consumed' ($script:inputQueue.Count -eq 0)

# --- Invoke-ItemRport ------------------------------------------------------------
$savedConfig = $SshConfig
$SshConfig = Join-Path $tmp 'no-block-config'
$threw = $false
try { Invoke-ItemRport } catch { $threw = $true }
Check 'rport item: errors without a block' $threw
$SshConfig = $savedConfig

'2400' | ForEach-Object { $script:inputQueue.Enqueue($_) }
Invoke-ItemRport
$raw = Get-Content -Raw $SshConfig
Check 'rport item: port updated'    ($raw.Contains('RemoteForward 127.0.0.1:2400 127.0.0.1:22'))
Check 'rport item: host preserved'  ($raw.Contains('HostName 198.51.100.77'))

$ReversePort = 2500
Invoke-ItemRport
Check 'rport item: parameter path' ((Get-Content -Raw $SshConfig).Contains('RemoteForward 127.0.0.1:2500 127.0.0.1:22'))
$ReversePort = 0

# Non-interactive base config on a fresh path: no RemoteForward
$SshConfig = Join-Path $tmp 'auto-config'
$ServerHost = '192.0.2.10'; $ServerUser = 'auto'
Invoke-ItemConfig
$raw = Get-Content -Raw $SshConfig
Check 'auto: host written'        ($raw.Contains('HostName 192.0.2.10'))
Check 'auto: no RemoteForward on fresh config' (-not $raw.Contains('RemoteForward'))
$ServerHost = ''; $ServerUser = ''
$SshConfig = $savedConfig

Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
exit $script:fail
