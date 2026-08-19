GO ?= go
CARGO ?= cargo
BIN ?= bin
CORPUS ?= corpus.jsonl
QUERIES ?= queries.txt
ROOT ?= corpus

GO_RUNNERS := bleve sqlitefts genba

.PHONY: all
all: build

.PHONY: build
build: $(BIN)/kura-bench $(BIN)/kura-corpus go-runners tantivy

$(BIN)/kura-bench: $(shell find cmd/kura-bench bench -name '*.go')
	$(GO) build -o $@ ./cmd/kura-bench

$(BIN)/kura-corpus: $(shell find cmd/kura-corpus corpus -name '*.go')
	$(GO) build -o $@ ./cmd/kura-corpus

.PHONY: go-runners
go-runners:
	@for r in $(GO_RUNNERS); do \
		echo "building $$r"; \
		$(GO) build -o $(BIN)/$$r-runner ./runners/$$r || exit 1; \
	done

# The Rust runner is optional. A machine without a Rust toolchain still produces
# a full table for every other engine, and the report says which ones ran.
.PHONY: tantivy
tantivy:
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) build --release --manifest-path runners/tantivy/Cargo.toml && \
		cp runners/tantivy/target/release/tantivy-runner $(BIN)/tantivy-runner; \
	else \
		echo "no cargo on this machine, skipping the tantivy runner"; \
	fi

.PHONY: test
test:
	$(GO) test ./...
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) test --manifest-path runners/tantivy/Cargo.toml; \
	fi

.PHONY: lint
lint:
	gofmt -l .
	$(GO) vet ./...
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) fmt --manifest-path runners/tantivy/Cargo.toml --check && \
		$(CARGO) clippy --manifest-path runners/tantivy/Cargo.toml --all-targets -- -D warnings; \
	fi

# Builds the corpus from checkouts under $(ROOT). This is the slow step and it
# only has to happen once per machine.
.PHONY: corpus
corpus: $(BIN)/kura-corpus
	$(BIN)/kura-corpus -root $(ROOT) -out $(CORPUS)

.PHONY: bench
bench: build
	$(BIN)/kura-bench -corpus $(CORPUS) -queries $(QUERIES) -bin $(BIN) -out results

.PHONY: clean
clean:
	rm -rf $(BIN)
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) clean --manifest-path runners/tantivy/Cargo.toml; \
	fi
