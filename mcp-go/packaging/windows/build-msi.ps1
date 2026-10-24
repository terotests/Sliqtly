# The .msi from the .exe build-windows.sh made, on Windows with WiX 5:
#
#   pwsh packaging/windows/build-msi.ps1 -Version 0.1.0 -Exe dist/windows_amd64/sliqtly-server.exe
#
# → dist/sliqtly-server_<version>_windows_amd64.msi
param(
  [Parameter(Mandatory)][string]$Version,
  [Parameter(Mandatory)][string]$Exe,
  [string]$Out = "dist"
)
$ErrorActionPreference = "Stop"
$here = $PSScriptRoot
# an .msi's version is three numbers; 0.1.4-rc1 is 0.1.4
$msiVersion = ($Version -split '[^0-9.]')[0]
if ($msiVersion -notmatch '^\d+\.\d+\.\d+$') { throw "version ${Version}: not x.y.z" }
if (-not (Get-Command wix -ErrorAction SilentlyContinue)) {
  dotnet tool install --global wix --version 5.0.2
  if ($LASTEXITCODE) { throw "dotnet tool install wix" }
  $env:PATH += ";$env:USERPROFILE\.dotnet\tools"
}
wix extension add -g WixToolset.Util.wixext/5.0.2
if ($LASTEXITCODE) { throw "wix extension add" }
New-Item -ItemType Directory -Force $Out | Out-Null
# README.txt with Windows' line ends, as in the .zip
$readme = Join-Path ([IO.Path]::GetTempPath()) "sliqtly-README.txt"
(Get-Content (Join-Path $here "README.txt")) -join "`r`n" | Set-Content -NoNewline -Encoding utf8 $readme
$msi = Join-Path $Out "sliqtly-server_${Version}_windows_amd64.msi"
wix build (Join-Path $here "Sliqtly.wxs") -arch x64 -ext WixToolset.Util.wixext `
  -d "Version=$msiVersion" -d "Exe=$((Resolve-Path $Exe).Path)" -d "Readme=$readme" -o $msi
if ($LASTEXITCODE) { throw "wix build" }
Write-Output $msi
