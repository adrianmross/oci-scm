PREFIX ?= $(HOME)/.local
VERSION ?= dev
LDFLAGS := -s -w -X github.com/adrianmross/oci-scm/internal/cli.version=$(VERSION)

.PHONY: build test vet fmt check install
build:
	go build -ldflags '$(LDFLAGS)' -o bin/oscm ./cmd/oci-scm
	go build -ldflags '$(LDFLAGS)' -o bin/oci-scm ./cmd/oci-scm
test:
	go test -race ./...
vet:
	go vet ./...
fmt:
	gofmt -w cmd internal
check: vet test
	test -z "$$(gofmt -l cmd internal)"
	actionlint
	goreleaser check
install: build
	install -d '$(PREFIX)/bin'
	install -m 755 bin/oscm '$(PREFIX)/bin/oscm'
	install -m 755 bin/oci-scm '$(PREFIX)/bin/oci-scm'
