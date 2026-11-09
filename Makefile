GO := $(if $(wildcard .tools/go/bin/go),.tools/go/bin/go,go)
NATIVE := $(CURDIR)/third_party
WHISPER := $(NATIVE)/whisper.cpp
WBUILD := $(NATIVE)/build-whisper
PORTAUDIO := $(NATIVE)/portaudio
PABUILD := $(NATIVE)/build-portaudio

export CGO_ENABLED := 1
export PKG_CONFIG_PATH := $(PABUILD):$(PKG_CONFIG_PATH)
export CGO_CFLAGS := -I$(WHISPER)/include -I$(WHISPER)/ggml/include
export CGO_LDFLAGS := -L$(WBUILD)/src -L$(WBUILD)/ggml/src -L$(WBUILD)/ggml/src/ggml-metal -L$(WBUILD)/ggml/src/ggml-blas
ifeq ($(shell uname -s),Darwin)
export MACOSX_DEPLOYMENT_TARGET := 14.2
export CGO_LDFLAGS += -framework Foundation -framework Metal -framework MetalKit -framework Accelerate -framework CoreAudio -framework AudioToolbox -framework AudioUnit -framework CoreServices
endif

.PHONY: build setup test test-ui bundle
build:
	$(GO) build -o tala ./cmd/tala

setup:
	bash scripts/native.sh

test:
	$(GO) test ./...

test-ui:
	node --test internal/dashboard/tests/*.test.cjs

bundle: build
	TALA_GO="$(GO)" bash scripts/bundle.sh
