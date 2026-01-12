# PowerShell Build Script

param(
    [Parameter(Position=0)]
    [string]$Target = "help"
)

$ErrorActionPreference = "Stop"

function Build-Relay {
    Write-Host "Building Relay server (Linux)..." -ForegroundColor Cyan
    Push-Location service/relay
    try {
        $env:GOOS = "linux"
        $env:GOARCH = "amd64"
        go build -o ../../bin/relay.linux main.go
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Relay build completed: bin/relay.linux" -ForegroundColor Green
        }
    } finally {
        Pop-Location
    }
}

function Build-Host {
    Write-Host "Building Host Agent..." -ForegroundColor Cyan
    Push-Location service/host
    try {
        # Check if goversioninfo is installed
        $hasVersionInfo = Get-Command go-winres -ErrorAction SilentlyContinue
        if ($hasVersionInfo -and (Test-Path "versioninfo.json")) {
            Write-Host "Adding version info..." -ForegroundColor Yellow
            go-winres make --in versioninfo.json
        }

        # Use ldflags to reduce file size
        $ldflags = "-s -w"
        go build -ldflags="$ldflags" -o ../../bin/host.exe main.go

        if ($LASTEXITCODE -eq 0) {
            Write-Host "Host build completed: bin/host.exe" -ForegroundColor Green
            Write-Host "Note: If deleted by antivirus, add to whitelist" -ForegroundColor Yellow
        }
    } finally {
        Pop-Location
    }
}

function Build-Client {
    Write-Host "Building Client..." -ForegroundColor Cyan
    if (-not (Test-Path "service/client")) {
        Write-Host "Warning: service/client directory not found, skipping" -ForegroundColor Yellow
        return
    }
    Push-Location service/client
    try {
        # $env:GOOS = "linux"
        # $env:GOARCH = "amd64"
        go build -o ../../bin/client main.go
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Client build completed: bin/client" -ForegroundColor Green
        }
    } finally {
        Pop-Location
    }
}

function Build-All {
    Write-Host "Building all components..." -ForegroundColor Cyan
    if (-not (Test-Path "bin")) {
        New-Item -ItemType Directory -Path "bin" | Out-Null
    }
    
    Build-Relay
    Build-Host
    Build-Client
    
    Write-Host ""
    Write-Host "All components built successfully!" -ForegroundColor Green
}

function Scp-Relay {
    Write-Host "Building and uploading Relay to server..." -ForegroundColor Cyan
    Build-Relay
    
    $remoteHost = "root@8.218.218.201"
    $remotePort = "22"
    $localFile = "bin/relay.linux"
    
    if (-not (Test-Path $localFile)) {
        Write-Host "Error: Build file not found: $localFile" -ForegroundColor Red
        return
    }
    
    Write-Host "Uploading to $remoteHost (port $remotePort)..." -ForegroundColor Cyan
    scp -P $remotePort $localFile "${remoteHost}:~/"
    
    if ($LASTEXITCODE -eq 0) {
        Write-Host "Upload successful!" -ForegroundColor Green
    } else {
        Write-Host "Upload failed" -ForegroundColor Red
    }
}

function Show-Help {
    Write-Host "Available commands:" -ForegroundColor Yellow
    Write-Host "  build-relay    - Build Relay server (Linux)"
    Write-Host "  build-host     - Build Host Agent (Linux)"
    Write-Host "  build-client   - Build Client (Linux)"
    Write-Host "  build-all       - Build all components"
    Write-Host "  scp-relay      - Build and upload Relay to server"
    Write-Host "  help           - Show this help"
    Write-Host ""
    Write-Host "Examples:"
    Write-Host "  .\build.ps1 build-relay"
    Write-Host "  .\build.ps1 build-all"
    Write-Host "  .\build.ps1 scp-relay"
}

# Main logic
$targetLower = $Target.ToLower()
switch ($targetLower) {
    "build-relay" { Build-Relay }
    "build-host" { Build-Host }
    "build-client" { Build-Client }
    "build-all" { Build-All }
    "scp-relay" { Scp-Relay }
    "help" { Show-Help }
    default {
        Write-Host "Unknown target: $Target" -ForegroundColor Red
        Show-Help
        exit 1
    }
}
