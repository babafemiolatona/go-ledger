-include .env
export

.PHONY: up down logs test lint seed migrate-up migrate-down

# Host-side URL (always localhost) — don't use .env's DATABASE_URL which may contain @db for containers
DATABASE_URL := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:5432/$(POSTGRES_DB)?sslmode=disable
MIGRATE ?= go run github.com/pressly/goose/v3/cmd/goose@latest -dir db/migrations postgres "$(DATABASE_URL)"

up:
	docker compose up --build

down:
	docker compose down

logs:
	docker compose logs -f api

test:
	go test ./... -race -count=1

lint:
	go vet ./...
	gofmt -l .

seed:
	docker compose exec -T db psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) -v ON_ERROR_STOP=1 -f - < db/seed.sql

migrate-up:
	@test -d db/migrations || (echo "no db/migrations yet (M1)"; exit 1)
	$(MIGRATE) up

migrate-down:
	@test -d db/migrations || (echo "no db/migrations yet (M1)"; exit 1)
	$(MIGRATE) down