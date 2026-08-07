set CGO_ENABLED=0

@REM === Windows (amd64) ===
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w" -o dist/server.exe ./cmd/server/

@REM === Linux (amd64) ===
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w" -o dist/server ./cmd/server/

@REM === UPX 压缩（可选，需要安装 UPX） ===
where upx >nul 2>nul
if %ERRORLEVEL% EQU 0 (
    upx --best --lzma dist/server.exe
    upx --best --lzma dist/server
)