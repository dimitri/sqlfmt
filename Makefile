BINARY   := bin/sqlfmt
WASM_DIR := dist/wasm
GO       := go
CORPUS   := $(shell find testdata/corpus -name '*.sql')

.PHONY: all build test wasm wasm-test clean

all: build

build:
	$(GO) build -o $(BINARY) ./cmd/sqlfmt

test: build
	$(GO) test ./...
	@echo "checking testdata/corpus/*.sql are already canonically formatted..."
	@dirty="$$($(BINARY) -l $(CORPUS))"; \
	if [ -n "$$dirty" ]; then \
		echo "sqlfmt -l reports files not in canonical form:"; \
		echo "$$dirty"; \
		exit 1; \
	fi

# Builds the browser WebAssembly module (globalThis.sqlfmt.format(sql)) plus
# the Go runtime's wasm_exec.js glue it needs, into $(WASM_DIR). See wasm/main.go.
wasm:
	mkdir -p $(WASM_DIR)
	GOOS=js GOARCH=wasm $(GO) build -o $(WASM_DIR)/sqlfmt.wasm ./wasm
	cp "$$($(GO) env GOROOT)/lib/wasm/wasm_exec.js" $(WASM_DIR)/wasm_exec.js

# Loads the built module under Node and exercises globalThis.sqlfmt.format,
# the same entry point a browser page would call. See wasm/smoketest.mjs.
wasm-test: wasm
	node wasm/smoketest.mjs

clean:
	rm -f $(BINARY)
	rm -rf $(WASM_DIR)
