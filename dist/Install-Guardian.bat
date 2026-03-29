@echo off
REM Guardian Agent Universal Installer Launcher
REM This batch file auto-detects OS architecture and runs the appropriate installer

setlocal

echo.
echo =====================================
echo   Guardian Universal Installer
echo =====================================
echo.

REM Check for administrator privileges
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo Requesting Administrator privileges...
    echo.
    
    REM Re-run this script with administrator privileges
    powershell -Command "Start-Process '%~f0' -Verb RunAs"
    exit /b
)

echo Running installer as Administrator...
echo.

REM Detect OS architecture
if "%PROCESSOR_ARCHITECTURE%"=="AMD64" (
    echo Detected: 64-bit system
    echo.
    PowerShell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0bin\Install-Agent64.ps1"
) else if "%PROCESSOR_ARCHITECTURE%"=="x86" (
    echo Detected: 32-bit system
    echo.
    PowerShell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0bin\Install-Agent32.ps1"
)

if %errorlevel% equ 0 (
    echo.
    echo Installation completed successfully!
) else (
    echo.
    echo Installation failed. Please check the error messages above.
)

echo.
pause
