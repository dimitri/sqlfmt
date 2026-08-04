BINARY  := bin/sqlfmt
GO      := go
CORPUS  := $(shell find testdata/corpus -name '*.sql')

.PHONY: all build test clean

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

clean:
	rm -f $(BINARY)
