VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test test-unit lint install clean

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

install:
	go install -ldflags '$(LDFLAGS)' .

clean:
	rm -f wyrm coverage.out
	rm -rf dist
