.PHONY: build test install clean

BINARY := tui-canvas
INSTALL_DIR := $(HOME)/.local/bin

build:
	go build -o $(BINARY) ./cmd/tui-canvas/

test:
	go test ./...

install: build
	install -m 755 $(BINARY) $(INSTALL_DIR)/$(BINARY)

clean:
	rm -f $(BINARY)
