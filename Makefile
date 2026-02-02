VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -ldflags "-s -w -X github.com/guzus/birdy/cmd.version=$(VERSION) -X github.com/guzus/birdy/cmd.commit=$(COMMIT) -X github.com/guzus/birdy/cmd.date=$(DATE)"

.PHONY: build install clean test

build:
	go build $(LDFLAGS) -o birdy .

install:
	go install $(LDFLAGS) .

clean:
	rm -f birdy

test:
	go test ./...
