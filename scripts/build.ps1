# Builds sing-box-tray natively on Windows (no `make` required).
$ErrorActionPreference = "Stop"

Set-Location (Join-Path $PSScriptRoot "..")

$Output = "build\sing_box_tray_runner.exe"

New-Item -ItemType Directory -Force -Path "build" | Out-Null

$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

go build -ldflags="-H windowsgui -s -w" -o $Output .

Write-Host "Built: $Output"
