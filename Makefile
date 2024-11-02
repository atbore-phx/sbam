VERSION=$(shell git describe --tags --always)
COMMIT=$(shell git rev-parse --short HEAD)
DATE=$(shell date)

.PHONY: build test test-build

test:
	go test -cover ./...

build:
	rm -rf bin
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'" -o bin/sbam

test-build: test build
