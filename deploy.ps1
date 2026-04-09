# Nightveil Deploy Script
# Usage: .\deploy.ps1 root@203.0.113.1
#
# Builds, uploads, installs, and starts the server on VPS.

param(
    [Parameter(Mandatory=$true)]
    [string]$Target  # e.g. root@203.0.113.1
)

$ErrorActionPreference = "Stop"

Write-Host ""
Write-Host "  ================================" -ForegroundColor Cyan
Write-Host "   Nightveil Deploy" -ForegroundColor Cyan
Write-Host "  ================================" -ForegroundColor Cyan
Write-Host ""

# Build
Write-Host "  [1/3] Building server binary..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:PATH += ";C:\Go\bin"
go build -o nv-linux ./cmd/nv/
if ($LASTEXITCODE -ne 0) { Write-Host "Build failed!" -ForegroundColor Red; exit 1 }
Write-Host "  [+] Built nv-linux" -ForegroundColor Green

# Upload
Write-Host "  [2/3] Uploading to $Target..." -ForegroundColor Yellow
scp nv-linux deploy/server.yaml deploy/cert.pem deploy/key.pem deploy/install.sh "${Target}:/root/"
if ($LASTEXITCODE -ne 0) { Write-Host "Upload failed!" -ForegroundColor Red; exit 1 }
Write-Host "  [+] Files uploaded" -ForegroundColor Green

# Install
Write-Host "  [3/3] Installing on server..." -ForegroundColor Yellow
ssh $Target "systemctl stop nightveil 2>/dev/null; cp /root/nv-linux /opt/nightveil/nv; chmod +x /opt/nightveil/nv; systemctl start nightveil; systemctl status nightveil --no-pager -l"
if ($LASTEXITCODE -ne 0) { Write-Host "Install failed!" -ForegroundColor Red; exit 1 }

Write-Host ""
Write-Host "  Deploy complete!" -ForegroundColor Green
Write-Host ""
