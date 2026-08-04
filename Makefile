BINARY   := bin/sqlfmt
WASM_DIR := dist/wasm
GO       := go
TINYGO   := tinygo
WASM_OPT := wasm-opt
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
# the wasm_exec.js glue it needs, into $(WASM_DIR), along with pre-compressed
# a pre-compressed sqlfmt.wasm.gz copy (wasm/compress.mjs) for callers who
# want the smaller transfer and are willing to decompress client-side --
# GitHub Releases doesn't serve Content-Encoding, so this isn't transparent
# to a plain fetch(). See the "WebAssembly build" section of README.md.
#
# Built with TinyGo (-no-debug -opt=z), then squeezed further with
# Binaryen's wasm-opt, rather than the standard `go build` toolchain: the
# same source compiles to ~330KB this way vs. ~2.9MB with stock Go (the
# standard toolchain's wasm output always statically links the full
# runtime/GC regardless of -ldflags, and cannot be told to drop it).
# Requires `tinygo`, `wasm-opt` (Binaryen), and `node` on PATH -- see the
# "WebAssembly build" section of README.md for install instructions.
wasm:
	mkdir -p $(WASM_DIR)
	$(TINYGO) build -o $(WASM_DIR)/sqlfmt.wasm -target=wasm -no-debug -opt=z ./wasm
	$(WASM_OPT) -Oz -o $(WASM_DIR)/sqlfmt.wasm $(WASM_DIR)/sqlfmt.wasm
	cp "$$($(TINYGO) env TINYGOROOT)/targets/wasm_exec.js" $(WASM_DIR)/wasm_exec.js
	node wasm/compress.mjs

# Loads the built module under Node and exercises globalThis.sqlfmt.format,
# the same entry point a browser page would call. See wasm/smoketest.mjs.
wasm-test: wasm
	node wasm/smoketest.mjs

clean:
	rm -f $(BINARY)
	rm -rf $(WASM_DIR)
