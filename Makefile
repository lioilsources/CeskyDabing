.PHONY: build test generate install deploy-spark

build:
	go build -o bin/dub ./cmd/dub

test:
	go test ./...

# sqlc (internal/store/db) + oapi-codegen (internal/workers/*)
generate:
	sqlc generate
	go generate ./...

install: build
	install -m 755 bin/dub /usr/local/bin/dub

# Zkopíruje repo na SPARK (build image dělá až úloha přes broker).
deploy-spark:
	rsync -az --delete --exclude .git --exclude bin --exclude '__pycache__' ./ spark:deploy/CeskyDabing/
