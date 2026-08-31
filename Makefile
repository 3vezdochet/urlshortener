.PHONY: build test test-integration vet vet-integration fmt-check run docker-up docker-down proto

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

# Requires DATABASE_URL, e.g. the one docker-up brings up on localhost:5432.
run:
	go run ./cmd/api

# Same, for the checker process (needs its own terminal — it doesn't exit).
run-checker:
	go run ./cmd/checker

# Full stack: postgres -> redis -> migrate (one-shot) -> api + checker,
# plus jaeger (traces, :16686), prometheus (:9091), grafana (:3000).
docker-up:
	docker compose -f deployments/docker/docker-compose.yml up --build

docker-down:
	docker compose -f deployments/docker/docker-compose.yml down -v

# Generates internal/ratelimit/grpcclient/ratelimitv1 from the .proto.
# Requires protoc, protoc-gen-go, protoc-gen-go-grpc on PATH — see README's
# "Rate limiting" section. Re-point api/ratelimit/v1/ratelimit.proto at your
# real rate limiter's contract first if it differs from the assumed one.
proto:
	protoc \
		--go_out=. --go_opt=module=urlshortener \
		--go-grpc_out=. --go-grpc_opt=module=urlshortener \
		api/ratelimit/v1/ratelimit.proto
