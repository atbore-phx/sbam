VERSION=$(shell git describe --tags --abbrev=0 --always)
COMMIT=$(shell git rev-parse --short HEAD)
DATE=$(shell date)

.PHONY: build test

test:
	go test ./...

build: test
	CGO_ENABLED=0 go build -ldflags="-X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'" -o bin/ha-fronius-bm