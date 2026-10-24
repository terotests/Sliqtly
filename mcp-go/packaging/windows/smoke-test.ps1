# Installs the .msi on this Windows computer, checks the service serves
# (MCP, a deck saved and drawn, the token, who can read the folder), that
# it stops and starts, an upgrade to -Upgrade (the same program as a newer
# version), and removes it again: what the Windows package workflow runs.
# As administrator:
#
#   pwsh packaging/windows/smoke-test.ps1 -Msi dist/a.msi -Upgrade dist/b.msi
param([Parameter(Mandatory)][string]$Msi, [Parameter(Mandatory)][string]$Upgrade)
$ErrorActionPreference = "Stop"
$home_ = Join-Path $env:ProgramData "Sliqtly"
$log = Join-Path $home_ "log\sliqtly.log"
$exe = Join-Path $env:ProgramFiles "Sliqtly\sliqtly-server.exe"
function Check($ok, $what) {
  if (-not $ok) {
    if (Test-Path $log) { Write-Output "--- $log"; Get-Content $log -Tail 60 }
    throw "FAILED: $what"
  }
  Write-Output "ok: $what"
}
function Msiexec($verb, $file, $logName) {
  # msiexec reads neither a relative path nor forward slashes reliably
  $file = (Resolve-Path $file).Path
  $logName = Join-Path (Get-Location).Path $logName
  $p = Start-Process msiexec.exe -Wait -PassThru -ArgumentList "$verb `"$file`" /qn /l*v `"$logName`""
  if ($p.ExitCode -ne 0) {
    Get-Content $logName | Select-String -Pattern "WixQuietExec|error|Error|return value 3" | Select-Object -Last 40
    throw "msiexec $verb $($file): exit $($p.ExitCode)"
  }
}
function WaitPort {
  for ($i = 0; $i -lt 60; $i++) {
    try { Invoke-WebRequest http://localhost:8080/ -SkipHttpErrorCheck -TimeoutSec 2 | Out-Null; return $true } catch { Start-Sleep 1 }
  }
  return $false
}
function Mcp($body, $token) {
  $h = @{ Accept = "application/json, text/event-stream" }
  if ($token) { $h.Authorization = "Bearer $token" }
  Invoke-WebRequest http://localhost:8080/mcp -Method Post -ContentType "application/json" -Headers $h -Body $body -SkipHttpErrorCheck
}

Msiexec "/i" $Msi "install.log"
Check ((Get-Service Sliqtly).Status -eq "Running") "service running"
$cfg = Get-CimInstance Win32_Service -Filter "Name='Sliqtly'"
Check ($cfg.StartName -eq "NT SERVICE\Sliqtly") "runs as NT SERVICE\Sliqtly ($($cfg.StartName))"
Check ($cfg.StartMode -eq "Auto") "starts with Windows"
Check (Test-Path (Join-Path $home_ "sliqtly.env")) "settings file written"
Check (WaitPort) "port 8080 answers"
$tokenFile = Join-Path $home_ "data\access-token"
Check (Test-Path $tokenFile) "access token made"
$token = (Get-Content $tokenFile -Raw).Trim()
$acl = (icacls $home_) -join "`n"
Write-Output $acl
Check (-not ($acl -match "BUILTIN\\Users|Authenticated Users|Everyone")) "only administrators, the system and the service can open the folder"

Check ((Mcp '{}' $null).StatusCode -eq 401) "no token: 401"
$init = Mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}' $token
Check ($init.StatusCode -eq 200 -and $init.Content -match "serverInfo") "MCP initialize"
$create = Mcp '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_presentation","arguments":{"title":"Smoke","markdown":"# Smoke\n\n## Two\n\nText"}}}' $token
Check ($create.Content -match "deck_id: (\w+)") "create_presentation"
$deck = $Matches[1]
$render = Mcp ('{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"render_slide","arguments":{"deck_id":"' + $deck + '","slide":2}}}') $token
Check ($render.Content -match '"type":"image"') "render_slide"
$page = Invoke-WebRequest "http://localhost:8080/s/$($deck)?token=$token" -SkipHttpErrorCheck
Check ($page.StatusCode -eq 200) "player page"
$editor = Invoke-WebRequest "http://localhost:8080/s/$($deck)?edit&token=$token" -SkipHttpErrorCheck
Check ($editor.StatusCode -eq 200) "editor page"

# stop waits for the server to end; the deck is there after a start
& $exe service restart
Check ($LASTEXITCODE -eq 0) "service restart"
Check ((Get-Content $log -Raw) -match "stopped") "the server stopped cleanly"
Check (WaitPort) "port answers after the restart"
$get = Mcp ('{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get_presentation","arguments":{"deck_id":"' + $deck + '"}}}') $token
Check ($get.Content -match "Smoke") "the deck kept over a restart"

# a newer version replaces it and keeps settings, token and decks
Add-Content (Join-Path $home_ "sliqtly.env") "`r`nSLIQTLY_MAX_FILE_MB=60"
Msiexec "/i" $Upgrade "upgrade.log"
Check ((Get-Service Sliqtly).Status -eq "Running") "service running after the upgrade"
# Programs and Features' entries (Win32_Product would check every package)
$products = Get-ItemProperty "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*" | Where-Object DisplayName -eq "Sliqtly server"
Check (@($products).Count -eq 1) "one Sliqtly server installed after the upgrade"
Check ((Get-Content (Join-Path $home_ "sliqtly.env") -Raw) -match "SLIQTLY_MAX_FILE_MB=60") "settings kept"
Check (WaitPort) "port answers after the upgrade"
Check ((Get-Content $tokenFile -Raw).Trim() -eq $token) "token kept"
$get = Mcp ('{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_presentation","arguments":{"deck_id":"' + $deck + '"}}}') $token
Check ($get.Content -match "Smoke") "the deck kept over the upgrade"

Msiexec "/x" $Upgrade "uninstall.log"
Check (-not (Get-Service Sliqtly -ErrorAction SilentlyContinue)) "service removed"
Check (-not (Test-Path "$env:ProgramFiles\Sliqtly\sliqtly-server.exe")) "program removed"
Check (Test-Path (Join-Path $home_ "data\sliqtly.db")) "decks kept after removal"
Write-Output "all good"
