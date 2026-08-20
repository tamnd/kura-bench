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
build: $(BIN)/kura-bench $(BIN)/kura-corpus $(BIN)/kura-queries $(BIN)/kura-relevance $(BIN)/kura-vectors $(BIN)/kura-vecbench $(BIN)/kura-graphs $(BIN)/kura-graphbench $(BIN)/kura-report go-runners rust-runners

$(BIN)/kura-bench: $(shell find cmd/kura-bench bench -name '*.go')
	$(GO) build -o $@ ./cmd/kura-bench

$(BIN)/kura-corpus: $(shell find cmd/kura-corpus corpus -name '*.go')
	$(GO) build -o $@ ./cmd/kura-corpus

$(BIN)/kura-queries: $(shell find cmd/kura-queries corpus -name '*.go')
	$(GO) build -o $@ ./cmd/kura-queries

$(BIN)/kura-relevance: $(shell find cmd/kura-relevance relevance bench -name '*.go')
	$(GO) build -o $@ ./cmd/kura-relevance

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

# Lucene is what most enterprise search actually runs on, since Elasticsearch
# and OpenSearch are Lucene with a cluster around them, so it belongs in the
# table. It is the one runner here that needs a Java compiler, and like ladybug
# it is built by its own target rather than by the default one. Everything else
# works without it.
JAVA ?= java
JAVAC ?= javac
LUCENE := runners/java/lucene
LUCENE_VERSION := $(shell sed -n 's/^[[:space:]]*"version": "\([^"]*\)".*/\1/p' $(LUCENE)/lucene.json | head -1)
LUCENE_JARS := $(abspath $(LUCENE)/$(LUCENE_VERSION))
LUCENE_CLASSES := $(abspath $(LUCENE)/classes)

.PHONY: lucene-jars
lucene-jars:
	$(LUCENE)/fetch.sh

# The two flags are the ones Lucene asks for at startup when it does not have
# them. Without the incubator module it falls back to scalar code in the places
# it has written vector code for, and without native access it prints a warning
# on every run about a restricted method that a later release will refuse
# outright. Both are what its own documentation tells an operator to pass, so
# running without them would measure a Lucene nobody deploys.
LUCENE_FLAGS := --add-modules jdk.incubator.vector --enable-native-access=ALL-UNNAMED

# The wrapper is what the orchestrator runs, because the contract is an
# executable named after the engine and a virtual machine needs a class path in
# front of it. It is written with absolute paths so that it works from whatever
# directory a run happens in.
.PHONY: lucene
lucene: lucene-jars
	$(JAVAC) -d $(LUCENE_CLASSES) -cp "$(LUCENE_JARS)/*" $(LUCENE)/Bench.java $(LUCENE)/Runner.java
	@mkdir -p $(BIN)
	@printf '#!/bin/sh\nexec %s %s -cp "%s:%s/*" Runner "$$@"\n' \
		"$(JAVA)" "$(LUCENE_FLAGS)" "$(LUCENE_CLASSES)" "$(LUCENE_JARS)" > $(BIN)/lucene-runner
	@chmod +x $(BIN)/lucene-runner
	@echo "built $(BIN)/lucene-runner against lucene $(LUCENE_VERSION)"

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

# Writes the query set for a corpus. queries.txt is about source code and asks
# about deadlocks and mmap, so running it against mail or an encyclopaedia
# measures nothing. Each corpus gets its own set, built from the corpus itself
# unless a real query log came down with it.
.PHONY: textqueries
textqueries: $(BIN)/kura-queries
	@if [ -f queries.dev.small.tsv ] && [ "$(TEXTSET)" = "msmarco" ]; then \
		$(BIN)/kura-queries -log queries.dev.small.tsv -out queries-$(TEXTSET).txt; \
	else \
		$(BIN)/kura-queries -corpus $(TEXTSET).jsonl -out queries-$(TEXTSET).txt; \
	fi

# The whole run against a downloaded corpus, which is the shortest way to a
# number on something other than source code.
#
# DEPTH is how many results each search asks for. Ten is a page and is what a
# latency number should be measured on. A hundred is what a first stage
# retriever has to return when something reranks behind it, and it is the depth
# to use when the answer wanted is recall rather than speed.
DEPTH ?= 10

.PHONY: textbench
textbench: build textqueries
	$(BIN)/kura-bench -corpus $(TEXTSET).jsonl -queries queries-$(TEXTSET).txt -bin $(BIN) -out results -depth $(DEPTH)

# Scores the answers rather than the latency, which only works on a corpus that
# came with judgments. Today that is the passage collection.
#
# It writes the run files that let another tool check the arithmetic and a JSON
# file of the scores. Point BASELINE at an earlier scores file to fail the target
# when a score has fallen further than the tolerance recorded in that file.
SCORES ?= scores.json
BASELINE ?=

.PHONY: relevance
relevance: $(BIN)/kura-relevance
	$(BIN)/kura-relevance -results results -qrels qrels.dev.small.tsv -queries queries.dev.small.tsv \
		-runs runs -out $(SCORES) -k $(DEPTH) $(if $(BASELINE),-baseline $(BASELINE))

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
	rm -rf $(BIN) $(LUCENE)/classes
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		$(CARGO) clean --manifest-path $(RUST)/Cargo.toml; \
	fi
