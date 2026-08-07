set CGO_ENABLED=0
@REM go build -o dist/server.exe ./cmd/server/

set GOOS=linux
set GOARCH=amd64
go build -o dist/server ./cmd/server/