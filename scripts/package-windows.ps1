# package-windows.ps1 -Version VERSION builds Goti for Windows amd64 and writes
#   dist\goti_<VERSION>_windows_amd64.zip        (Goti.exe, goti-cli.exe, LICENSE.txt)
#   dist\Goti_<VERSION>_windows_amd64_setup.exe  (NSIS installer)
# Needs Go and NSIS 3 (makensis on PATH or in "C:\Program Files (x86)\NSIS").
param([Parameter(Mandatory = $true)][string]$Version)
$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$Arch = 'amd64'
$Winres = 'github.com/tc-hib/go-winres@v0.3.3'

function Invoke-Native {
    & $args[0] $args[1..($args.Length - 1)]
    if ($LASTEXITCODE -ne 0) { throw "$($args[0]) failed with exit code $LASTEXITCODE" }
}

# Numeric a.b.c.d: drop a leading "v" and any pre-release/build suffix.
$core = ($Version -replace '^v', '') -replace '[-+].*$', ''
$parts = @($core.Split('.') | ForEach-Object { if ($_ -match '^\d+$') { [int]$_ } else { 0 } })
while ($parts.Count -lt 4) { $parts += 0 }
$VersionNum = ($parts[0..3]) -join '.'

$dist = Join-Path $PWD 'dist'
$stage = Join-Path $dist 'stage-windows'
if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
New-Item -ItemType Directory -Force $stage | Out-Null

$syso = "cmd/goti/rsrc_windows_$Arch.syso"
try {
    Invoke-Native go run $Winres make --in packaging/windows/winres.json --out cmd/goti/rsrc --arch $Arch `
        --product-version $VersionNum --file-version $VersionNum
    $env:CGO_ENABLED = '0'; $env:GOOS = 'windows'; $env:GOARCH = $Arch
    $ldflags = "-s -w -X main.version=$Version"
    Invoke-Native go build -trimpath -ldflags "$ldflags -H windowsgui" -o "$stage\Goti.exe" ./cmd/goti
    Invoke-Native go build -trimpath -ldflags $ldflags -o "$stage\goti-cli.exe" ./cmd/goti-cli
} finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $syso
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
}
Invoke-Native go run ./packaging/cmd/pesubsystem -want 2 "$stage\Goti.exe"
Invoke-Native go run ./packaging/cmd/pesubsystem -want 3 "$stage\goti-cli.exe"

Copy-Item LICENSE "$stage\LICENSE.txt"
$zip = Join-Path $dist "goti_${Version}_windows_${Arch}.zip"
if (Test-Path $zip) { Remove-Item -Force $zip }
Compress-Archive -Path "$stage\Goti.exe", "$stage\goti-cli.exe", "$stage\LICENSE.txt" -DestinationPath $zip

$makensis = (Get-Command makensis -ErrorAction SilentlyContinue).Source
if (-not $makensis) { $makensis = Join-Path ${env:ProgramFiles(x86)} 'NSIS\makensis.exe' }
if (-not (Test-Path $makensis)) { throw 'makensis (NSIS 3) not found' }
$setup = Join-Path $dist "Goti_${Version}_windows_${Arch}_setup.exe"
Invoke-Native $makensis /V3 /WX /INPUTCHARSET UTF8 "/DVERSION=$Version" "/DVERSION_NUM=$VersionNum" "/DARCH=$Arch" `
    "/DSRCDIR=$stage" "/DOUTFILE=$setup" packaging\windows\installer.nsi

Remove-Item -Recurse -Force $stage
Get-ChildItem $zip, $setup
