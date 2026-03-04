
build:
	@CGO_ENABLED=1 go build -tags fts5 -o bin/atombase

amd64:
	@CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="zig cc -target x86_64-linux-musl" CXX="zig c++ -target x86_64-linux-musl" go build -tags fts5 -o bin/atombase

run: build
	@./bin/atombase

test:
	@CGO_ENABLED=1 go test -tags fts5 -v ./...

test-integration:
	@CGO_ENABLED=1 go test -tags "fts5 integration" -v ./database/...
