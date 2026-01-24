# ProcSentinel Agent Service Uninstallation Script
# This script removes the ProcSentinel agent service and optionally removes files

param(
    [string]$InstallPath = "C:\Windows\System32\ProcSentinel\agent",
    [switch]$RemoveFiles = $true,
    [switch]$Force = $false
)

# Check if running as administrator
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")
if (-not $isAdmin) {
    Write-Host "ERROR: This script requires Administrator privileges!" -ForegroundColor Red
    Write-Host "Please right-click PowerShell and select 'Run as Administrator', then run this script again." -ForegroundColor Yellow
    exit 1
}

Write-Host ""
Write-Host "=====================================" -ForegroundColor Cyan
Write-Host "  Guardian 64bit Uninstaller" -ForegroundColor Cyan
Write-Host "=====================================" -ForegroundColor Cyan
Write-Host ""

try {
    $exePath = Join-Path $InstallPath "procsentinel-agent64.exe"

    # Check if service exists
    Write-Host "[1/3] Checking for service..." -ForegroundColor Green
    $service = Get-Service -Name "ProcSentinelAgent" -ErrorAction SilentlyContinue
    if (-not $service) {
        Write-Host "      Guardian not found" -ForegroundColor Yellow
        if (-not $RemoveFiles) {
            Write-Host ""
            Write-Host "No service to remove. Use -RemoveFiles to remove installation files." -ForegroundColor Gray
            exit 0
        }
    } else {
        Write-Host "      Service found: $($service.DisplayName)" -ForegroundColor Gray
        
        # Stop service if running
        if ($service.Status -eq "Running") {
            Write-Host "      Stopping service..." -ForegroundColor Gray
            Stop-Service -Name "ProcSentinelAgent" -Force -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 2
            
            # Verify service stopped
            $service = Get-Service -Name "ProcSentinelAgent" -ErrorAction SilentlyContinue
            if ($service -and $service.Status -eq "Running") {
                if ($Force) {
                    Write-Host "      WARNING: Guardian still running, proceeding anyway..." -ForegroundColor Yellow
                } else {
                    throw "Guardian failed to stop. Use -Force to proceed anyway."
                }
            } else {
                Write-Host "      Guardian stopped successfully" -ForegroundColor Green
            }
        } else {
            Write-Host "      Guardian already stopped" -ForegroundColor Gray
        }

        # Remove service
        Write-Host ""
        Write-Host "[2/3] Removing service..." -ForegroundColor Green
        if (Test-Path $exePath) {
            $removeResult = & $exePath -remove 2>&1
            Start-Sleep -Seconds 2
            
            if ($LASTEXITCODE -ne 0) {
                Write-Host "      WARNING: Guardian removal returned exit code: $LASTEXITCODE" -ForegroundColor Yellow
            } else {
                Write-Host "      Guardian removed successfully" -ForegroundColor Green
            }
        } else {
            Write-Host "      WARNING: Executable not found at $exePath" -ForegroundColor Yellow
            Write-Host "      Guardian may still be registered in Windows" -ForegroundColor Gray
        }
    }

    # Remove files if requested
    if ($RemoveFiles) {
        Write-Host ""
        Write-Host "[3/3] Removing installation files..." -ForegroundColor Green
        
        if (Test-Path $InstallPath) {
            # List files being removed
            $files = Get-ChildItem -Path $InstallPath -Recurse -ErrorAction SilentlyContinue
            if ($files) {
                Write-Host "      Files to be removed:" -ForegroundColor Gray
                $files | Select-Object -First 10 | ForEach-Object { Write-Host "        $($_.Name)" -ForegroundColor DarkGray }
                if ($files.Count -gt 10) {
                    Write-Host "        ... and $($files.Count - 10) more files" -ForegroundColor DarkGray
                }
            }

            Write-Host ""
            Write-Host "      Removing files..." -ForegroundColor Gray
            Remove-Item -Path $InstallPath -Recurse -Force -ErrorAction Stop
            Write-Host "      Installation files removed successfully" -ForegroundColor Green
        } else {
            Write-Host "      Installation directory not found: $InstallPath" -ForegroundColor Yellow
        }
    } else {
        Write-Host ""
        Write-Host "[3/3] Skipping file removal" -ForegroundColor Yellow
        Write-Host "      Files remain at: $InstallPath" -ForegroundColor Gray
        Write-Host "      Use -RemoveFiles to delete them" -ForegroundColor Gray
    }

    # Verify service is gone
    Start-Sleep -Seconds 2
    $service = Get-Service -Name "ProcSentinelAgent" -ErrorAction SilentlyContinue
    
    Write-Host ""
    Write-Host "=====================================" -ForegroundColor Cyan
    
    if ($service) {
        Write-Host "  Uninstallation Completed (with warnings)" -ForegroundColor Yellow
        Write-Host "=====================================" -ForegroundColor Cyan
        Write-Host ""
        Write-Host "WARNING: Guardian may still be registered in Windows" -ForegroundColor Yellow
        Write-Host "You may need to:" -ForegroundColor Gray
        Write-Host "  1. Restart your system" -ForegroundColor Gray
        Write-Host "  2. Or manually remove it using: sc.exe delete ProcSentinelAgent" -ForegroundColor Gray
    } else {
        Write-Host "  Uninstallation Complete!" -ForegroundColor Green
        Write-Host "=====================================" -ForegroundColor Cyan
        Write-Host ""
        Write-Host "[OK] Service uninstalled successfully" -ForegroundColor Green
    }
    
    Write-Host ""
    if (-not $RemoveFiles) {
        Write-Host "Installation files were preserved at:" -ForegroundColor Gray
        Write-Host "  $InstallPath" -ForegroundColor White
        Write-Host ""
        Write-Host "To remove files, run:" -ForegroundColor Gray
        Write-Host "  .\Uninstall-Agent.ps1 -RemoveFiles" -ForegroundColor White
    } else {
        Write-Host "All installation files have been removed" -ForegroundColor Green
    }
    Write-Host ""

} catch {
    Write-Host ""
    Write-Host "=====================================" -ForegroundColor Red
    Write-Host "  Uninstallation Failed!" -ForegroundColor Red
    Write-Host "=====================================" -ForegroundColor Red
    Write-Host ""
    Write-Host "Error: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ""
    Write-Host "You can try:" -ForegroundColor Yellow
    Write-Host "  1. Run with -Force parameter" -ForegroundColor Gray
    Write-Host "  2. Stop the service manually: Stop-Service ProcSentinelAgent" -ForegroundColor Gray
    Write-Host "  3. Remove service manually: sc.exe delete ProcSentinelAgent" -ForegroundColor Gray
    Write-Host ""
    exit 1
}
