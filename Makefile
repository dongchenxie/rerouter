APP=a-site
BIN=dist/$(APP)

.PHONY: build run clean fmt

build:
	@mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o $(BIN) .

run:
	@B_BASE_URL?=https://your-b-site.example.com
	@echo "Running with B_BASE_URL=$${B_BASE_URL}"
	LISTEN_ADDR=:8080 CACHE_DIR=./cache go run .

clean:
	rm -rf dist cache tmp

fmt:
	go fmt ./...

