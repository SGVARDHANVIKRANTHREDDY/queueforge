.PHONY: all build test test-integration test-chaos test-load migrate run-api run-worker clean

all: build test

build:
	go build -o bin/api.exe ./cmd/api
	go build -o bin/worker.exe ./cmd/worker
	go build -o bin/admin.exe ./cmd/admin

test:
	go test -v ./tests/...

test-integration:
	go test -v ./tests/integration/...

test-chaos:
	go test -v ./tests/chaos/...

test-load:
	go test -v ./tests/load/...

migrate:
	go run ./cmd/admin migrate

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

docker-up:
	docker compose -f deploy/docker-compose.yml up --build

docker-down:
	docker compose -f deploy/docker-compose.yml down

clean:
	rm -rf bin/
