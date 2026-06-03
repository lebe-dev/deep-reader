# --- Variables ---
version := `tr -d '[:space:]' < VERSION 2>/dev/null || echo "0.0.0"`
imageName := 'tinyops/deep-reader'

# --- Utility ---
default:
    @just --list

cleanup:
    rm -f ./bin/deep-reader

# --- Dependencies ---
bump-backend-deps:
    go get -u ./...
    go mod tidy

bump-frontend-deps:
    cd frontend && npm update

bump-deps: bump-backend-deps && bump-frontend-deps

# --- Build ---
build-frontend:
    cd frontend && npm install && npm pkg set version="{{ version }}" && npm run build
    rm -rf web/dist
    mkdir -p web/dist
    cp -r frontend/build/. web/dist/
    touch web/dist/.gitkeep

build: build-frontend && format
    go build -ldflags="-s -w -X deep-reader/internal/version.Version={{ version }}" -o ./bin/deep-reader ./cmd/server

# --- Lints ---
lint-backend: format
    go vet ./...
    golangci-lint run ./cmd/... ./internal/...

lint-frontend:
    cd frontend && npm run lint && npm run check

lint: format
    just lint-backend
    just lint-frontend

# --- Tests ---
test-backend name="":
    #!/usr/bin/env sh
    if [ -z "{{ name }}" ]; then
        go test ./...
    else
        go test ./... -run "{{ name }}"
    fi

test-frontend:
    cd frontend && npm run check && npm run test

test: test-backend && test-frontend

# --- Coverage ---
coverage:
    go test ./... -coverprofile=coverage.out
    go tool cover -func=coverage.out
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report generated at coverage.html"

# --- Format ---
format:
    go fmt ./...
    cd frontend && npm run format

# --- Development ---
run-backend:
    go run ./cmd/server

run-frontend:
    cd frontend && npm run dev

start-env: stop-env
    docker compose up -d

stop-env:
    docker compose down

logs:
    docker compose logs -f

reset-env: stop-env
    @rm -rf data
    @echo "Dev environment data removed. Run 'just start-env' to restart."

# --- Image ---
build-image: test && lint
    docker build --progress=plain --platform linux/amd64 -t {{ imageName }}:{{ version }} .

push-image:
    docker push {{ imageName }}:{{ version }}

release-image: build-image && push-image

release: release-image

deploy:
    ssh kaiman "cd /opt/deep-reader && sed -i 's|{{ imageName }}:[^\"]*|{{ imageName }}:{{ version }}|' docker-compose.yml && docker compose pull && docker compose down && docker compose up -d"

ssh:
    ssh kaiman
