@echo off
cd /d "%~dp0"
echo Starting Zeus Broadcast Orchestrator UI Proxy...
echo Open http://localhost:8082 in your browser
python ui_proxy.py
pause
