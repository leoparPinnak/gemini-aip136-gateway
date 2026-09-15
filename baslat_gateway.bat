@echo off
chcp 65001 >nul
title Gemini AIP-136 Protocol Gateway (Port 8000)

echo ========================================================
echo   ⚡ Gemini AIP-136 Protocol Gateway
echo   Port: 8000 ^| TLS: Go Native crypto/tls
echo ========================================================
echo.

cd /d "%~dp0"

if not exist dsh_go_gateway.exe (
    echo [BILGI] dsh_go_gateway.exe bulunamadi, derleniyor...
    go build -o dsh_go_gateway.exe .
)

dsh_go_gateway.exe -port 8000
pause
