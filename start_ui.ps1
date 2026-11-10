# Zeus Broadcast Orchestrator - UI Proxy Startup
# Runs the Python proxy on Windows so the UI bypasses the WSL VM entirely.

$ErrorActionPreference = "Stop"
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
Set-Location $scriptDir

Write-Host "================================================" -ForegroundColor Cyan
Write-Host "  Zeus Broadcast Orchestrator - UI Proxy" -ForegroundColor Cyan
Write-Host "================================================" -ForegroundColor Cyan
Write-Host " Listening: http://0.0.0.0:8082" -ForegroundColor Green
Write-Host " K8s API:   https://127.0.0.1:44837 (kind via port-forward)" -ForegroundColor Green
Write-Host " Open:      http://localhost:8082" -ForegroundColor Green
Write-Host "================================================" -ForegroundColor Cyan

python ui_proxy.py
