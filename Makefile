.PHONY: web build test run dev-db dev-db-down docker

web:
	cd web && npm ci && npm run build
	touch web/dist/.gitkeep

build: web
	CGO_ENABLED=0 go build -trimpath -o bin/dbyson ./cmd/dbyson

test:
	go vet ./...
	go test ./...

run: build
	./bin/dbyson

dev-db:
	docker compose -f docker-compose.dev.yml up -d mysql mariadb

dev-db-down:
	docker compose -f docker-compose.dev.yml down

docker:
	docker build -t dbyson:dev .
