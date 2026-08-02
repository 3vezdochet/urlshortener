.PHONY: build test vet fmt-check

build:
	go build ./...

test:
	go test ./... -race -cover

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needs to be run on:"; gofmt -l .; exit 1)
