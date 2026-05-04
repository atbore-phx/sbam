COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null)

# Get branch name; in detached HEAD this returns "HEAD" or empty.
BRANCH=$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null)

# If we're on main, prefer a Git tag when available; otherwise normalize branch name.
# If branch is empty or detached (HEAD), fallback to commit short.
ifeq ($(BRANCH),main)
VERSION=$(shell git describe --tags --always 2>/dev/null || echo $(COMMIT))
else
ifeq ($(BRANCH),HEAD)
VERSION=$(COMMIT)
else
ifeq ($(BRANCH),)
VERSION=$(COMMIT)
else
VERSION=$(shell printf '%s\n' "$(BRANCH)" | sed -E 's|/|-|g; s|[^A-Za-z0-9._-]|-|g; s|-+|-|g; s|^-||; s|-$$||')
endif
endif
endif

DATE=$(shell date)

.PHONY: build test test-build

make fmt:
	go fmt ./...

test:
	go test -cover ./...

build:
	rm -rf bin
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.date=$(DATE)'" -o bin/sbam

test-build: test build

all: fmt test-build
