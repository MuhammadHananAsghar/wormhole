VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
BINARY := wormhole
BUILD_DIR := dist

.PHONY: all build install clean test test-all

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/wormhole

install: build
	sudo cp $(BINARY) /usr/local/bin/$(BINARY)
	@echo "Installed $(BINARY) to /usr/local/bin/$(BINARY)"

test:
	go test ./... -race

test-all: test
	cd edge && npm test

clean:
	rm -rf $(BINARY) $(BUILD_DIR)

# Cross-compilation targets
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

dist: clean
	@mkdir -p $(BUILD_DIR)
	@$(foreach platform,$(PLATFORMS),\
		$(eval OS := $(word 1,$(subst /, ,$(platform))))\
		$(eval ARCH := $(word 2,$(subst /, ,$(platform))))\
		$(eval EXT := $(if $(filter windows,$(OS)),.exe,))\
		echo "Building $(OS)/$(ARCH)..." && \
		GOOS=$(OS) GOARCH=$(ARCH) go build -ldflags "$(LDFLAGS)" \
			-o $(BUILD_DIR)/$(BINARY)-$(OS)-$(ARCH)$(EXT) ./cmd/wormhole && \
	) true

.PHONY: dist
