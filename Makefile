GO ?= go
CARGO ?= cargo
BIN ?= bin
CORPUS ?= corpus.jsonl
QUERIES ?= queries.txt
ROOT ?= corpus

GO_RUNNERS := bleve sqlitefts genba
RUST_RUNNERS := tantivy seekstorm
RUST := runners/rust

.PHONY: all
all: build

.PHONY: build
build: $(BIN)/kura-bench $(BIN)/kura-corpus go-runners rust-runners

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

# The Rust runners are optional. A machine without a Rust toolchain still
# produces a full table for every other engine, and the report says which ones
# ran. They live in one cargo workspace so that they share the measuring code.
.PHONY: rust-runners
rust-runners:
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) build --release --manifest-path $(RUST)/Cargo.toml && \
		for r in $(RUST_RUNNERS); do \
			cp $(RUST)/target/release/$$r-runner $(BIN)/$$r-runner || exit 1; \
		done; \
	else \
		echo "no cargo on this machine, skipping the rust runners"; \
	fi

.PHONY: test
test:
	$(GO) test ./...
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) test --manifest-path $(RUST)/Cargo.toml; \
	fi

.PHONY: lint
lint:
	gofmt -l .
	$(GO) vet ./...
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) fmt --manifest-path $(RUST)/Cargo.toml --check && \
		$(CARGO) clippy --manifest-path $(RUST)/Cargo.toml --all-targets -- -D warnings; \
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
		$(CARGO) clean --manifest-path $(RUST)/Cargo.toml; \
	fi
