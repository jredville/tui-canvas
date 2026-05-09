.PHONY: build test install clean dist release

BINARY     := tui-canvas
INSTALL_DIR := $(HOME)/.local/bin
VERSION    ?= $(shell git describe --tags --always --dirty)
LDFLAGS    := -ldflags "-X main.Version=$(VERSION)"
PLATFORMS  := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/tui-canvas/

test:
	go test ./...

install: build
	install -m 755 $(BINARY) $(INSTALL_DIR)/$(BINARY)

clean:
	rm -f $(BINARY)
	rm -rf dist/

dist:
	mkdir -p dist
	$(foreach PLATFORM,$(PLATFORMS), \
	  GOOS=$(word 1,$(subst /, ,$(PLATFORM))) \
	  GOARCH=$(word 2,$(subst /, ,$(PLATFORM))) \
	  go build $(LDFLAGS) -o dist/tui-canvas-$(subst /,-,$(PLATFORM)) ./cmd/tui-canvas/ ;)

release: dist
	gh release create $(VERSION) dist/* \
	  --title "$(VERSION)" \
	  --notes "Release $(VERSION)"
	printf '%s\n' "$(VERSION)" > plugin/VERSION
