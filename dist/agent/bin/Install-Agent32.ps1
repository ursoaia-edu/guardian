# Guardian Agent - Windows Installer
# This script builds the agent and installs it as a Windows service

param(
    [string]$InstallPath = "C:\Windows\System32\ProcSentinel\agent",
    [string]$EnvFile = "",
    [switch]$StartService = $true,
    [switch]$SkipBuild = $false
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
Write-Host "  Guardian 64bit Installer" -ForegroundColor Cyan
Write-Host "=====================================" -ForegroundColor Cyan
Write-Host ""

# Function to read .env file
function Read-EnvFile {
    param([string]$Path)

    $envVars = @{}

    if (Test-Path $Path) {
        Get-Content $Path | ForEach-Object {
            $line = $_.Trim()
            if ($line -and -not $line.StartsWith("#")) {
                if ($line -match "^([^=]+)=(.*)$") {
                    $key = $matches[1].Trim()
                    $value = $matches[2].Trim()
                    $envVars[$key] = $value
                }
            }
        }
    }

    return $envVars
}

try {
    $projectRoot = Split-Path $PSScriptRoot -Parent

    # # Step 1: Read configuration from .env file
    Write-Host "[1/8] Reading configuration..." -ForegroundColor Green

    # Default to project root .env if not specified
    if (-not $EnvFile) {
        $EnvFile = Join-Path $projectRoot "agent.env"
    }

    $envConfig = @{}
    $serverAddress = "http://localhost:8080"  # Default fallback

    if (Test-Path $EnvFile) {
        Write-Host "      Reading from: $EnvFile" -ForegroundColor Gray
        $envConfig = Read-EnvFile -Path $EnvFile

        if ($envConfig.ContainsKey("SERVER_ADDRESS")) {
            $serverAddress = $envConfig["SERVER_ADDRESS"]
            Write-Host "      Server Address: $serverAddress" -ForegroundColor Green
        } else {
            Write-Host "      WARNING: SERVER_ADDRESS not found in .env, using default: $serverAddress" -ForegroundColor Yellow
        }
    } else {
        Write-Host "      WARNING: .env file not found at: $EnvFile" -ForegroundColor Yellow
        Write-Host "      Using default server address: $serverAddress" -ForegroundColor Yellow
    }

    # Step 2: Verify or build executable
    Write-Host ""
    Write-Host "[2/8] Locating agent executable..." -ForegroundColor Green

    # Try to find the agent executable in common locations
    $possiblePaths = @(
        (Join-Path $projectRoot "bin\agent\procsentinel-agent32.exe"),
        (Join-Path $projectRoot "agent\procsentinel-agent32.exe"),
        (Join-Path (Split-Path $projectRoot -Parent) "agent\procsentinel-agent32.exe")
    )

    $exePath = $null
    foreach ($path in $possiblePaths) {
        if (Test-Path $path) {
            $exePath = $path
            Write-Host "      Found executable: $projectRoot" -ForegroundColor Green
            break
        }
    }

    if (-not $exePath) {
        if ($SkipBuild) {
            throw "Agent executable not found and -SkipBuild specified. Please build the agent first."
        }

        # Try to build it
        Write-Host "      Executable not found, attempting to build..." -ForegroundColor Yellow

        # Find agent source directory
        $agentSrcPath = Join-Path (Split-Path $projectRoot -Parent) "agent"
        if (-not (Test-Path $agentSrcPath)) {
            throw "Agent source directory not found at: $agentSrcPath"
        }
    }

    if (-not (Test-Path $exePath)) {
        throw "Agent executable still not found at: $exePath"
    }

    # Step 3: Create installation directory
    Write-Host ""
    Write-Host "[3/8] Setting up installation directory..." -ForegroundColor Green
    if (-not (Test-Path $InstallPath)) {
        New-Item -ItemType Directory -Path $InstallPath -Force | Out-Null
        Write-Host "      Created: InstallPath" -ForegroundColor Gray
    } else {
        Write-Host "      Using existing: InstallPath" -ForegroundColor Gray
    }

    # Step 4: Stop and remove existing service if it exists
    Write-Host ""
    Write-Host "[4/8] Checking for existing service..." -ForegroundColor Green
    $service = Get-Service -Name "ProcSentinelAgent" -ErrorAction SilentlyContinue

    if ($service) {
        Write-Host "      Found existing service" -ForegroundColor Yellow

        if ($service.Status -eq "Running") {
            Write-Host "      Stopping service..." -ForegroundColor Gray
            Stop-Service -Name "ProcSentinelAgent" -Force -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 2
        }

        $oldExePath = Join-Path $InstallPath "procsentinel-agent32.exe"
        if (Test-Path $oldExePath) {
            Write-Host "      Removing old service registration..." -ForegroundColor Gray
            & $oldExePath -remove 2>&1 | Out-Null
            Start-Sleep -Seconds 2
        }
    } else {
        Write-Host "      No existing service found" -ForegroundColor Gray
    }

    # Step 5: Copy files to installation directory
    Write-Host ""
    Write-Host "[5/8] Copying files..." -ForegroundColor Green

    # Copy executable
    Copy-Item -Path $exePath -Destination $InstallPath -Force
    Write-Host "      Copied guardian.exe" -ForegroundColor Gray

    # Request IDENTITY from user
    $identityInput = Read-Host "Enter IDENTITY (optional, must be integer)"
    $identityValue = $null

    if ($identityInput -and $identityInput -match "^\d+$") {
        $identityValue = $identityInput
    } elseif ($identityInput) {
        Write-Host "      WARNING: Invalid IDENTITY value. Must be an integer. Skipping IDENTITY." -ForegroundColor Yellow
    }

    # Create .env configuration file with all settings from source
    $targetEnvPath = Join-Path $InstallPath ".env"

    if ($envConfig.Count -gt 0) {
        # Use existing .env configuration
        $envContent = ""
        foreach ($key in $envConfig.Keys) {
            $envContent += "$key=$($envConfig[$key])`r`n"
        }
        if ($identityValue) {
            $envContent += "IDENTITY=$identityValue`r`n"
        }
        Set-Content -Path $targetEnvPath -Value $envContent.TrimEnd() -Encoding Ascii
        Write-Host "      Copied .env configuration from source" -ForegroundColor Gray
    } else {
        # Create default .env
        $envContent = @"
SERVER_ADDRESS=$serverAddress
WEB_DIR=web
"@
        if ($identityValue) {
            $envContent += "`r`nIDENTITY=$identityValue"
        }
        Set-Content -Path $targetEnvPath -Value $envContent -Encoding Ascii
        Write-Host "      Created default .env configuration" -ForegroundColor Gray
    }

    # Step 6: Install as Windows service
    Write-Host ""
    Write-Host "[6/8] Installing Windows service..." -ForegroundColor Green

    $installedExePath = Join-Path $InstallPath "procsentinel-agent32.exe"

    & $installedExePath -install

    if ($LASTEXITCODE -eq 0) {
        Write-Host "      SUCCESS: Service installed" -ForegroundColor Green
    } else {
        throw "Failed to install service (exit code: $LASTEXITCODE)"
    }

    # Step 7: Start the service if requested
    if ($StartService) {
        Write-Host ""
        Write-Host "[7/8] Starting service..." -ForegroundColor Green

        & $installedExePath -start

        if ($LASTEXITCODE -eq 0) {
            Write-Host "      SUCCESS: Service started" -ForegroundColor Green

            # Verify service is running
            Start-Sleep -Seconds 3
            $service = Get-Service -Name "ProcSentinelAgent" -ErrorAction SilentlyContinue
            if ($service -and $service.Status -eq "Running") {
                Write-Host "      Service is running" -ForegroundColor Green
            } else {
                Write-Host "      WARNING: Service may not be running properly" -ForegroundColor Yellow
                Write-Host "      Check Windows Event Log (Application) for details" -ForegroundColor Gray
            }
        } else {
            Write-Host "      WARNING: Service installed but failed to start (exit code: $LASTEXITCODE)" -ForegroundColor Yellow
            Write-Host "      You can start it manually using: services.msc" -ForegroundColor Gray
        }
    } else {
        Write-Host ""
        Write-Host "[7/8] Skipping service start" -ForegroundColor Yellow
    }

    # Step 8: Display summary
    Write-Host ""
    Write-Host "[8/8] Done" -ForegroundColor Green

} catch {
    Write-Host ""
    Write-Host "ERROR: Installation failed!" -ForegroundColor Red
    Write-Host "Error: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ""
    exit 1
}
