GO ?= go
CARGO ?= cargo
BIN ?= bin
CORPUS ?= corpus.jsonl
QUERIES ?= queries.txt
ROOT ?= corpus

GO_RUNNERS := bleve sqlitefts genba
# The graph runners are written as directory:name, because the binary carries
# the engine's name in the report and the directory carries the suite it
# belongs to.
GO_GRAPHRUNNERS := sqlitegraph:sqlite
RUST_RUNNERS := kura tantivy seekstorm
RUST_VECRUNNERS := exact turbovec hnsw
RUST_GRAPHRUNNERS := csr petgraph
RUST := runners/rust

# ladybug is a C++ shared library, so its runner is the one thing here that
# needs cgo and a library fetched ahead of time. It is built by its own target
# rather than by the default one, and everything else works without it.
LADYBUG := third_party/ladybug
LADYBUG_VERSION := $(shell sed -n 's/^[[:space:]]*"version": "\([^"]*\)".*/\1/p' $(LADYBUG)/ladybug.json | head -1)
LADYBUG_DIR := $(abspath $(LADYBUG)/$(LADYBUG_VERSION)/$(shell $(GO) env GOOS)-$(shell $(GO) env GOARCH))

DATA ?= vecdata
DATASET ?= sift
METRIC ?= euclidean

GRAPHDATA ?= graphdata
GRAPH ?= ca-grqc

.PHONY: all
all: build

.PHONY: build
build: $(BIN)/kura-bench $(BIN)/kura-corpus $(BIN)/kura-vectors $(BIN)/kura-vecbench $(BIN)/kura-graphs $(BIN)/kura-graphbench $(BIN)/kura-report go-runners rust-runners

$(BIN)/kura-bench: $(shell find cmd/kura-bench bench -name '*.go')
	$(GO) build -o $@ ./cmd/kura-bench

$(BIN)/kura-corpus: $(shell find cmd/kura-corpus corpus -name '*.go')
	$(GO) build -o $@ ./cmd/kura-corpus

$(BIN)/kura-vectors: $(shell find cmd/kura-vectors vectors -name '*.go')
	$(GO) build -o $@ ./cmd/kura-vectors

$(BIN)/kura-vecbench: $(shell find cmd/kura-vecbench bench vectors -name '*.go')
	$(GO) build -o $@ ./cmd/kura-vecbench

$(BIN)/kura-graphs: $(shell find cmd/kura-graphs graphs -name '*.go')
	$(GO) build -o $@ ./cmd/kura-graphs

$(BIN)/kura-graphbench: $(shell find cmd/kura-graphbench bench graphs -name '*.go')
	$(GO) build -o $@ ./cmd/kura-graphbench

$(BIN)/kura-report: $(shell find cmd/kura-report bench graphs -name '*.go')
	$(GO) build -o $@ ./cmd/kura-report

# Rebuild every report from the results already on disk, which is what you want
# after rerunning a single engine.
.PHONY: report
report: $(BIN)/kura-report
	$(BIN)/kura-report -results results

.PHONY: go-runners
go-runners:
	@for r in $(GO_RUNNERS); do \
		echo "building $$r"; \
		$(GO) build -o $(BIN)/$$r-runner ./runners/$$r || exit 1; \
	done
	@for r in $(GO_GRAPHRUNNERS); do \
		dir=$${r%%:*}; name=$${r##*:}; \
		echo "building $$name-graphrunner"; \
		$(GO) build -o $(BIN)/$$name-graphrunner ./runners/$$dir || exit 1; \
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
		done && \
		for r in $(RUST_VECRUNNERS); do \
			cp $(RUST)/target/release/$$r-vecrunner $(BIN)/$$r-vecrunner || exit 1; \
		done && \
		for r in $(RUST_GRAPHRUNNERS); do \
			cp $(RUST)/target/release/$$r-graphrunner $(BIN)/$$r-graphrunner || exit 1; \
		done; \
	else \
		echo "no cargo on this machine, skipping the rust runners"; \
	fi

# Fetches the pinned shared library for this machine. It reaches the network,
# so it is a separate target from the build and never runs during a benchmark.
.PHONY: ladybug-lib
ladybug-lib:
	$(LADYBUG)/fetch.sh

# The rpath is what lets the binary find the library without an environment
# variable at run time, which matters because the orchestrator starts the
# runners itself.
.PHONY: ladybug
ladybug: ladybug-lib
	CGO_ENABLED=1 \
	CGO_CFLAGS="-I$(LADYBUG_DIR)" \
	CGO_LDFLAGS="-L$(LADYBUG_DIR) -Wl,-rpath,$(LADYBUG_DIR)" \
	$(GO) build -tags ladybug -o $(BIN)/ladybug-graphrunner ./runners/ladybug

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

# Downloads a published corpus and builds it. Source code is one shape of text
# and these are the others, so a number measured on one of them is a different
# fact from the same number measured on the checkouts. The archive is
# checksummed and kept, so running this again costs nothing.
TEXTSET ?= enron
CACHE ?= cache

.PHONY: textset
textset: $(BIN)/kura-corpus
	$(BIN)/kura-corpus -dataset $(TEXTSET) -cache $(CACHE) -limit $(TEXTLIMIT) -out $(TEXTSET).jsonl

# Zero means all of them. It is a variable rather than a hardcoded zero because
# the passage collection is nine million documents and not every machine that
# wants a latency number has room for it.
TEXTLIMIT ?= 0

.PHONY: bench
bench: build
	$(BIN)/kura-bench -corpus $(CORPUS) -queries $(QUERIES) -bin $(BIN) -out results

# Fetches the vector dataset. Half a gigabyte for SIFT and four for GIST, and
# like the corpus it only has to happen once per machine.
.PHONY: vectors
vectors: $(BIN)/kura-vectors
	$(BIN)/kura-vectors -dataset $(DATASET) -metric $(METRIC) -out $(DATA)

.PHONY: vecbench
vecbench: build
	$(BIN)/kura-vecbench -dataset $(DATASET) -metric $(METRIC) -data $(DATA) -bin $(BIN) -out results

# Fetches the graph and works out the answers every runner is checked against.
# Like the corpus and the vectors it only has to happen once per machine.
.PHONY: graphs
graphs: $(BIN)/kura-graphs
	$(BIN)/kura-graphs -dataset $(GRAPH) -out $(GRAPHDATA)

.PHONY: graphbench
graphbench: build
	$(BIN)/kura-graphbench -dataset $(GRAPH) -data $(GRAPHDATA) -bin $(BIN) -out results

.PHONY: clean
clean:
	rm -rf $(BIN)
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) clean --manifest-path $(RUST)/Cargo.toml; \
	fi
