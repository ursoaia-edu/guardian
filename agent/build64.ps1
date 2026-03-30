Write-Host "Cleaning..." -ForegroundColor Yellow
go clean -cache

# Build
Write-Host "Building..." -ForegroundColor Yellow
$env:CGO_ENABLED=1
$env:GOARCH="amd64"
go build -trimpath -o ..\dist\bin\agent\procsentinel-agent64.exe

