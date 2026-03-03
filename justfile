build:
    go build -o example/ ./cmd/hledger-build/

test:
    go test -v -race ./...

lint:
    golangci-lint fmt
    go vet ./...
    golangci-lint run

demo:
    rm -rf example
    just build
    (cd example && ./hledger-build init)

