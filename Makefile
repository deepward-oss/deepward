GO        ?= go
BIN       ?= bin/deepward
PKG       := github.com/deepward-oss/deepward
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE      ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.Date=$(DATE)

.PHONY: build dist snapshot image test lint cover cover-check integration licenses tidy clean

PLATFORMS ?= linux/amd64 linux/arm64 darwin/arm64 windows/amd64
IMAGE     ?= deepward:$(VERSION)

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/deepward

dist:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
		echo "building dist/deepward_$${os}_$${arch}$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/deepward_$${os}_$${arch}$$ext ./cmd/deepward || exit 1; \
	done

# Local dry run of the release: archives, checksums, SBOMs and the image,
# nothing is published. Needs goreleaser v2, syft and Docker with buildx.
snapshot:
	goreleaser release --snapshot --clean

image:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t $(IMAGE) .

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

cover-check: cover
	$(GO) run ./internal/tools/covercheck -profile coverage.out

integration:
	$(GO) test -tags integration -count=1 ./test/integration/...

licenses:
	$(GO) run github.com/google/go-licenses/v2@v2.0.1 check ./... \
		--allowed_licenses=MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC,MPL-2.0

tidy:
	$(GO) mod tidy

clean:
	rm -rf bin dist coverage.out coverage.html
