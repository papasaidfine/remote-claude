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

Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
exit $script:fail
