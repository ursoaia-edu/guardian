Write-Host "Cleaning..." -ForegroundColor Yellow
go clean -cache

# Build
$env:GOOS="windows"
$env:GOARCH="386"
Write-Host "Building..." -ForegroundColor Yellow
$env:CGO_ENABLED=1
go build -trimpath -o ..\dist\bin\agent\procsentinel-agent32.exe

