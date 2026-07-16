VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -ldflags "-s -w -X github.com/guzus/birdy/cmd.version=$(VERSION) -X github.com/guzus/birdy/cmd.commit=$(COMMIT) -X github.com/guzus/birdy/cmd.date=$(DATE)"

.PHONY: build install clean test test-e2b test-race vet verify release-notes

build:
	go build $(LDFLAGS) -o birdy .

install:
	go install $(LDFLAGS) .

clean:
	rm -f birdy

test:
	go test ./... -count=1

test-e2b:
	node --test e2b-runner/config.test.mjs

test-race:
	go test -race ./tui ./cmd ./internal/birdbox ./internal/claude ./internal/state ./internal/store -count=1

vet:
	go vet ./...

verify: vet test test-e2b test-race

release-notes:
	./scripts/release_notes.sh "$${TAG:-HEAD}"
