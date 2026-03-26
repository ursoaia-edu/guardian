# ProcSentinel Whitelist Editor - test runner
#
# Plain "go test" covers the parsing and file logic. The window smoke test needs
# Common Controls 6.0 linked in, so this script embeds the asInvoker test
# manifest for the duration of the run and removes it again afterwards -- a
# leftover .syso would silently end up in the release binary too.

param(
    [switch]$SkipGui = $false
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$syso = "rsrc_windows_$(go env GOARCH).syso"

try {
    if (-not $SkipGui) {
        Write-Host "Generating test manifest resource ..." -ForegroundColor Yellow
        go run github.com/akavel/rsrc@v0.10.2 -manifest whitelist-gui-test.manifest -arch (go env GOARCH) -o $syso
        if ($LASTEXITCODE -ne 0) { throw "rsrc failed" }

        $env:PS_GUI_TEST = "1"
    } else {
        Remove-Item Env:\PS_GUI_TEST -ErrorAction SilentlyContinue
    }

    Write-Host "Running tests ..." -ForegroundColor Yellow
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "tests failed" }

    Write-Host ""
    Write-Host "All tests passed." -ForegroundColor Green
} catch {
    Write-Host ""
    Write-Host "FAILED: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
} finally {
    if (Test-Path $syso) { Remove-Item $syso -Force }
    Remove-Item Env:\PS_GUI_TEST -ErrorAction SilentlyContinue
}
