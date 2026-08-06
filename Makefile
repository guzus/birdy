VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -ldflags "-s -w -X github.com/guzus/birdy/cmd.version=$(VERSION) -X github.com/guzus/birdy/cmd.commit=$(COMMIT) -X github.com/guzus/birdy/cmd.date=$(DATE)"

.PHONY: build install clean test test-e2b test-race vet verify release-notes e2b-deps

build:
	go build $(LDFLAGS) -o birdy .

install:
	go install $(LDFLAGS) .

clean:
	rm -f birdy

test:
	go test ./... -count=1

# The runner's tests import `e2b`, so they need node_modules present. CI installs
# it as a separate step; on a fresh local checkout `make verify` used to fail
# here with ERR_MODULE_NOT_FOUND and look like a real break.
e2b-deps:
	@test -d e2b-runner/node_modules || npm ci --prefix e2b-runner --omit=dev --ignore-scripts

test-e2b: e2b-deps
	npm test --prefix e2b-runner

test-race:
	go test -race ./tui ./cmd ./internal/birdbox ./internal/claude ./internal/state ./internal/store -count=1

vet:
	go vet ./...

verify: vet test test-e2b test-race

release-notes:
	./scripts/release_notes.sh "$${TAG:-HEAD}"
