VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test test-unit lint vulncheck cover install clean docs-install docs-serve docs-build

build:
	go build -ldflags '$(LDFLAGS)' -o wyrm .

test:
	go test ./...

# Unit tests only (skips the tmux integration test)
test-unit:
	go test -short ./...

lint:
	golangci-lint run
	test -z "$$(gofmt -l .)"

# Same advisory check CI runs, for before you push. Version and GOTOOLCHAIN are
# pinned to match .github/workflows/ci.yml — the scanner needs a newer Go than
# wyrm does, and a local run that quietly used a different one is how the CI
# failure got missed in the first place.
vulncheck:
	GOTOOLCHAIN=auto go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...

# Coverage plus the floor CI enforces, so a local run fails the same way.
cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	./.github/scripts/check-coverage.sh coverage.out

install:
	go install -ldflags '$(LDFLAGS)' .

clean:
	rm -f wyrm coverage.out
	rm -rf dist site

docs-install:
	pip install -r requirements-docs.txt

docs-serve:
	mkdocs serve

docs-build:
	mkdocs build
