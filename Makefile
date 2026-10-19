# fmtt – Podcast-Transkripte in HTML umwandeln
#
#   make            optimiertes Binary nach bin/fmtt bauen
#   make install    optimiert bauen und nach $(GOBIN) installieren
#   make uninstall  installiertes Binary entfernen
#   make test       Tests für beide Eingabeformate ausführen
#   make vet        gofmt-Prüfung und go vet
#   make clean      bin/ löschen

BINARY  := fmtt
PKG     := ./cmd/$(BINARY)
BIN_DIR := bin

# Zielordner wie bei "go install": GOBIN, sonst GOPATH/bin.
GOBIN := $(shell go env GOBIN)
ifeq ($(strip $(GOBIN)),)
GOBIN := $(shell go env GOPATH)/bin
endif

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# -trimpath: keine lokalen Pfade im Binary
# -s -w:     ohne Symbol- und Debug-Tabellen (ca. 1/3 kleiner)
# -X:        Version für "fmtt -version"
BUILD_FLAGS := -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build install uninstall test vet clean

all: build

build:
	go build $(BUILD_FLAGS) -o $(BIN_DIR)/$(BINARY) $(PKG)
	@echo "Gebaut: $(BIN_DIR)/$(BINARY) ($(VERSION))"

install:
	GOBIN="$(GOBIN)" go install $(BUILD_FLAGS) $(PKG)
	@echo "Installiert: $(GOBIN)/$(BINARY) ($(VERSION))"

uninstall:
	rm -f "$(GOBIN)/$(BINARY)"

test:
	go test ./...

vet:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "Nicht gofmt-formatiert:"; echo "$$unformatted"; exit 1; fi
	go vet ./...

clean:
	rm -rf $(BIN_DIR)
