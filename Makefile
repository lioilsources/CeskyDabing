export CGO_ENABLED = 0

PY_SOURCES = $(shell git ls-files -co --exclude-standard 'workers/*.py')
GENERATED_FILES = $(wildcard internal/store/db/*.go internal/workers/*/client.gen.go)

.PHONY: build test generate gate install deploy-spark

build:
	go build -o bin/dub ./cmd/dub

test:
	go test ./...

# sqlc (internal/store/db) + oapi-codegen (internal/workers/*)
generate:
	sqlc generate
	go generate ./...

# Gate before every push. Generated code must already match its sources.
gate:
	@before=$$(cat $(GENERATED_FILES) | shasum); $(MAKE) -s generate; \
	  [ "$$before" = "$$(cat $(GENERATED_FILES) | shasum)" ] || { echo "generated code was stale, regenerated: review and commit"; exit 1; }
	@test -z "$$(gofmt -l $$(git ls-files -co --exclude-standard '*.go'))" || { gofmt -l $$(git ls-files -co --exclude-standard '*.go'); echo "gofmt"; exit 1; }
	go vet ./...
	go test ./...
	python3 -m py_compile $(PY_SOURCES)
	go build -o bin/dub ./cmd/dub

install: build
	install -m 755 bin/dub /usr/local/bin/dub

# Copy the repo to SPARK (images are built by a broker job, not here).
deploy-spark:
	rsync -az --delete --exclude .git --exclude bin --exclude run --exclude models --exclude scratch \
		--exclude '__pycache__' ./ spark:deploy/CeskyDabing/
