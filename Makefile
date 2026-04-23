COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null)

# Get branch name; in detached HEAD this returns "HEAD" or empty.
BRANCH=$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)

# If we're on main, prefer a Git tag when available; otherwise normalize branch name.
ifeq ($(BRANCH),main)
VERSION=$(shell git describe --tags --always 2>/dev/null)
else
VERSION=$(shell echo $(BRANCH) | sed -E 's|/|-|g; s|[^A-Za-z0-9._-]|-|g; s|-+|-|g; s|^-||; s|-$$||')
endif

DATE=$(shell date)

.PHONY: build test test-build

test:
	go test -cover ./...

build:
	rm -rf bin
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'" -o bin/sbam

test-build: test build
