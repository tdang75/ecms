.PHONY: test test-unit up build

# Run the full test suite against the running docker-compose postgres.
# Requires: docker compose up (postgres must be reachable on localhost:5432).
test:
	cd backend && go test ./... -v -timeout 120s

# Run only the pure unit tests (no database required).
test-unit:
	cd backend && go test ./... -v -run "TestEnsureOfficeExt|TestHasPerm|TestGetEnv|TestWriteJSON|TestWriteError|TestJWT" -timeout 30s

# Build and start all services.
up:
	docker compose up --build

# Build images only.
build:
	docker compose build
