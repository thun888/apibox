set CGO_ENABLED=0

@REM 获取 Git Tag 作为版本号（无 tag 时用 "dev"）
for /f "delims=" %%i in ('git describe --tags --abbrev^=0 2^>nul') do set VERSION=%%i
if "%VERSION%"=="" set VERSION=dev
echo Version: %VERSION%

@REM === Windows (amd64) ===
set GOOS=windows
set GOARCH=amd64
go build -ldflags="-s -w -X github.com/thun888/apibox/internal/api.Version=%VERSION%" -o dist/server.exe ./cmd/server/

@REM === Linux (amd64) ===
set GOOS=linux
set GOARCH=amd64
go build -ldflags="-s -w -X github.com/thun888/apibox/internal/api.Version=%VERSION%" -o dist/server ./cmd/server/

@REM === UPX 压缩（可选，需要安装 UPX） ===
where upx >nul 2>nul
if %ERRORLEVEL% EQU 0 (
    upx --best --lzma dist/server.exe
    upx --best --lzma dist/server
)