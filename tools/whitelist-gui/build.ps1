# Guardian Console - build script
#
# Produces exactly one self-contained GUI executable, Guardian.exe, with an
# embedded manifest that requests administrator rights.
#
# It is deliberately a 32-bit build: a 64-bit executable cannot start on 32-bit
# Windows, while a 32-bit one runs on both through WOW64. It detects the OS
# architecture at run time and installs the matching agent, which makes it the
# universal installer. Its own file access goes through ioPath(), which rewrites
# System32 to Sysnative so the file system redirector does not send it to
# SysWOW64.
#
# There is no second, native artifact on purpose: one shipped binary means one
# thing to distribute, and one fewer guessable name in the agent's protected
# process list.
#
# guardian.ico is generated from guardian-mark.png by tools/mkico. See README
# for how to regenerate it.

param(
    [string]$OutDir = ""
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not $OutDir) {
    $OutDir = Join-Path $PSScriptRoot "..\..\dist\agent"
}
if (-not (Test-Path $OutDir)) {
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
}

function Build-One {
    param([string]$GoArch, [string]$OutName)

    # The .syso is architecture specific and Go picks it up by file name suffix.
    # It is deleted again afterwards: the linker adds it to *every* binary in the
    # package, including the test binary, which would then demand elevation and
    # make "go test" impossible to run.
    $syso = "rsrc_windows_$GoArch.syso"

    try {
        Write-Host "Generating $syso ..." -ForegroundColor Yellow
        go run github.com/akavel/rsrc@v0.10.2 -manifest whitelist-gui.manifest -ico guardian.ico -arch $GoArch -o $syso
        if ($LASTEXITCODE -ne 0) { throw "rsrc failed for $GoArch" }

        Write-Host "Building $OutName ($GoArch) ..." -ForegroundColor Yellow
        $env:GOOS = "windows"
        $env:GOARCH = $GoArch
        $env:CGO_ENABLED = "0"

        $out = Join-Path $OutDir $OutName
        go build -trimpath -ldflags "-s -w -H windowsgui" -o $out
        if ($LASTEXITCODE -ne 0) { throw "go build failed for $GoArch" }

        Write-Host "      -> $out" -ForegroundColor Green
    } finally {
        if (Test-Path $syso) { Remove-Item $syso -Force }
    }
}

try {
    Build-One -GoArch "386" -OutName "Guardian.exe"

    Write-Host ""
    Write-Host "Build complete." -ForegroundColor Green
} catch {
    Write-Host ""
    Write-Host "Build failed: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
