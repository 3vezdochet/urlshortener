.PHONY: build test test-integration vet vet-integration fmt-check

build:
	go build ./...

test:
	go test ./... -race -cover

# Spins up its own Postgres container via testcontainers-go — needs a local
# Docker daemon, no manual DB setup required.
test-integration:
	go test -tags=integration ./test/integration/... -v

vet:
	go vet ./...

vet-integration:
	go vet -tags=integration ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needs to be run on:"; gofmt -l .; exit 1)
