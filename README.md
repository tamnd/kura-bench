# kura-bench

A benchmark suite for search engines, built to produce numbers that survive being argued with.

Every engine here indexes the same data in the same order, is searched with the same queries, and is measured by the operating system rather than by its own stopwatch.
Nothing is simulated and nothing is generated.
The corpus is real source trees, the vectors are the published SIFT and GIST descriptors with the ground truth that came with them, the queries are the sort of thing people actually type, and every figure in a report came from a process that really did the work.

There are two suites.
`kura-bench` measures full text engines and `kura-vecbench` measures vector indexes.
They share the machine description, the process counters and the report writer, because two engines timed by two pieces of code are not being compared to each other.

## Why it exists

The first version of this was not a repository at all, it was a script that pointed each engine at a directory of checkouts and timed it.
The numbers it produced were nonsense.
A first pass over a checkout on a Windows machine ran two orders of magnitude slower than a second pass over the same files, and no amount of staring at the engine explained the difference, because the difference was not in the engine.
It was the filesystem being touched for the first time.

So the corpus became one JSON lines file.
Reading it is sequential, it fits in the page cache, and the same file can be copied to another machine and produce a comparable result.
What is left after that is the engine.

## What is measured, in the text suite

Indexing, and how it scales with the corpus.
Documents per second, megabytes of text per second, wall time, user and system CPU time separately, the parallelism the engine actually achieved, peak resident memory, and the bytes it read and wrote to get there.

Storage.
The size of the index on disk, the number of files it is spread over, and the ratio of index size to the size of the text that went into it.

Cold start.
Opening an index that was written by a different process, which is what a restart looks like.

Search, one query at a time.
Every query is run several times and reported as a minimum, a median, a 95th percentile and a maximum, along with how many documents it matched and how much CPU each query cost.

Search with several in flight.
Throughput in queries per second with a configurable number of workers, which is where an engine that holds a global lock stops looking good.

Incremental update.
Reindexing five thousand documents into an index that is already open and already being searched, and what that does to the size on disk.

Machine.
Every result carries the host, the CPU, the core count, the memory, the load average before the run started and the free memory at that moment.
A run on a machine that was already busy is marked as not dedicated, and the report says so, because a number taken on a loaded box is worth reporting and is not worth comparing.

## What is measured, in the vector suite

Everything above that still applies: build time and its parallelism, index size on disk against the size of the raw float32 vectors, cold start in a second process, single query latency, and throughput with several in flight.

What is different is accuracy.
A text engine either found the document or it did not, and an approximate vector index is only as fast as it is inaccurate.
So an index does not have one speed, it has a curve, and a single number taken from anywhere on that curve can be made to say whatever you want.
Each runner is asked to search at several settings, reports the recall it reached at each, and the report compares the engines at equal recall rather than at each engine's favourite setting.

Recall is scored against exact ground truth.
For Euclidean that is the file published with the dataset, computed by somebody else, which makes the exact scan's recall a real check on this whole suite: it has to come back at one, and anything else means the files are being read wrongly and every other figure is wrong the same way.
Cosine and inner product have no published ground truth, so it is computed once here by a full exact scan on every core and cached next to the dataset.

The metric is a first class part of a result and not a footnote.
A quantizing index built for maximum inner product, scored against Euclidean ground truth, comes back at about a tenth and looks like a bad index.
It is not a bad index, it is answering a different question.
Every runner is told which metric the run is under and refuses the run if it cannot answer that one, so a mismatch is an error rather than a number.

## Engines

Text:

| Engine | Language | What it is |
| --- | --- | --- |
| bleve | Go | The scorch index, the usual answer for full text search inside a Go binary |
| sqlitefts | Go | SQLite FTS5 through a pure Go driver, the thing most projects reach for first |
| tantivy | Rust | A fast Lucene style index, the number worth being measured against |
| seekstorm | Rust | A newer memory mapped index making strong latency claims, worth checking |
| genba | Go | Our own index, the reason the rest of this exists |

Vector:

| Engine | Language | Metrics | What it is |
| --- | --- | --- | --- |
| exact | Rust | all three | The brute force scan, the thing every approximate index is measured against |
| hnsw | Rust | euclidean, cosine | The graph index, at the connection and construction settings hnswlib has defaulted to for years |
| turbovec | Rust | inner product | A quantizing index, one index per bit width because the width is fixed when it is built |

Each engine is a separate binary that speaks a small contract: create, add a batch, flush, open, search, close.
Adding one is a hundred lines and does not touch the harness.
The Go runners live under `runners/`, one directory each, and the Rust runners share a cargo workspace at `runners/rust` so that the same measuring code times all of them.
A text runner is called `<engine>-runner` and a vector runner `<engine>-vecrunner`, both built into `bin/`, and each suite only ever picks up its own.

Every engine is pinned to a version and `kura-versions` compares each pin against its registry.
A workflow runs it every Monday and opens an issue when something has fallen behind, because a benchmark against a two year old release is a benchmark against nothing.

	go run ./cmd/kura-versions

## How a run works

Each engine is run twice, as two separate processes.

The first process builds the index and exits.
The second process opens it, searches it, runs the concurrent phase and the update phase, and exits.
Splitting them is the only way the cold start figure means anything, because an engine that has just written an index has all of it in its own caches.

Every Go engine is handed documents in batches of exactly five hundred.
Engines all have an opinion about the right batch size and every one of those opinions is different, so letting each engine pick its own would measure the opinions.
A benchmark where each subject brings its own stopwatch measures the stopwatches.

Queries are normalised to OR across every engine so that hit counts are comparable.
Bleve is told `MatchQueryOperatorOr`, FTS5 gets an explicit `OR` between terms because a bare list of terms in FTS5 means AND, and the others already score every term.
Every engine stores the document body, so the index size comparison is like for like.

## Running it

```sh
make build
```

That builds both orchestrators, the corpus builder, the dataset fetcher and every runner into `bin/`.
The Rust runners are skipped with a message if there is no cargo on the machine, and the report says which engines ran.

Build a corpus from checkouts you name:

```sh
bin/kura-corpus -repo linux=/src/linux -repo llvm=/src/llvm-project -out corpus.jsonl
```

Or from a directory that holds one checkout per subdirectory:

```sh
bin/kura-corpus -root ~/corpus -out corpus.jsonl
```

The named form is the one to use for a result anybody else is going to read, because a corpus that means the same thing on four machines has to be built from the same named projects.
Building it is the slowest step in the repository and it only has to happen once per machine.

Then run everything:

```sh
bin/kura-bench -corpus corpus.jsonl -queries queries.txt -bin bin -out results
```

This writes `results/<engine>-<host>.json` for each engine and `results/report-<host>.md` alongside them.
The JSON is the record, the markdown is what a person reads.

Useful flags:

- `-engines bleve,genba` runs a subset instead of everything found in `-bin`.
- `-limit 200000` stops after that many documents, which is how a shared machine gets a run that finishes.
- `-repeat 50` runs each query that many times, the default is twenty.
- `-workers 16` sets the concurrency for the several-in-flight phase, the default is the core count.
- `-keep` leaves the built indexes in place instead of deleting them.

## Running the vector suite

Fetch a dataset.
The addresses, the sizes and the checksums are pinned in `vectors/dataset.go`, so two machines can prove they ran the same numbers:

```sh
bin/kura-vectors -dataset sift -out vecdata
```

`siftsmall` is five megabytes and is what continuous integration runs, `sift` is half a gigabyte and `gist` is four.
For cosine or inner product the same command computes the ground truth and caches it next to the dataset, which is a full exact scan and is the slow part:

```sh
bin/kura-vectors -dataset sift -metric inner-product -out vecdata
```

Then run the engines:

```sh
bin/kura-vecbench -dataset sift -metric euclidean -data vecdata -bin bin -out results
```

This writes `results/vec-<engine>-<dataset>-<metric>-<host>.json` and `results/vector-report-<dataset>-<metric>-<host>.md`.
The dataset and the metric are in the name because three runs of the same engines on one machine are three different measurements.

Useful flags:

- `-metric euclidean|cosine|inner-product` picks what nearest means, and an engine that cannot answer that question refuses the run instead of reporting a meaningless recall.
- `-engines exact,hnsw` runs a subset.
- `-k 10` is how many neighbours are asked for and the depth recall is scored at.
- `-limit 100000` indexes part of the base set, which is how a shared machine gets a run that finishes. Recall then becomes a lower bound, because the ground truth still covers the whole set, and the report says so.
- `-queries 1000` uses part of the query set.

## The query set

`queries.txt` is grouped by intent, because the interesting thing is not the average, it is where the engines disagree.
There are very common terms that match most of the corpus, ordinary two and three word queries, rare identifiers that match a handful of documents, prose terms that only appear in documentation, and one worst case single word.
An engine that is quick on rare terms and slow on common ones has a different problem from one that is uniformly slow, and an average would hide both.

## Reading a result

Two things are worth knowing before comparing numbers from two files.

Peak resident memory is a process wide monotone maximum, so a per phase peak is not recoverable from it.
What is reported is the peak as it stood at the end of each phase, and the report says that.

Some engines have no on disk form.
The genba runner today uses an in memory store, so its open phase is a full reindex from the corpus.
That is the honest cold start for an in memory store rather than a favour done to it, and it is written down in the result notes rather than left for somebody to work out.

## Continuous integration

Every pull request builds and tests on Linux and macOS with the race detector on, cross compiles for Windows, Linux arm64 and macOS arm64, builds and clippies the Rust crate on all three platforms, and then does a real end to end run.
The smoke run builds a corpus out of this repository's own checkout and runs every engine over it.
It is a small corpus and the numbers from it are not worth publishing, but it exercises the corpus builder, every runner, both phases, the merge and the report.
A harness that only ever ran on a developer's laptop is a harness that has silently stopped working.

The vector suite gets the same treatment on siftsmall, under both Euclidean and inner product.
That run asserts one figure rather than printing it: the exact scan has to score exactly one against the published ground truth, and the job fails if it does not.
It is the only number in the suite with a known right answer, so it is the one worth checking on every pull request.

## Installing

Every tagged release publishes archives for Linux, macOS and Windows on amd64 and arm64, deb, rpm and apk packages, and a container image on GHCR.
The archive contains the orchestrator, the corpus builder, every Go runner and the query set, so unpacking it on a machine is the whole setup.

```sh
docker run --rm -v "$PWD:/bench" ghcr.io/tamnd/kura-bench:latest \
  -corpus /bench/corpus.jsonl -queries /bench/queries.txt -bin /usr/bin -out /bench/results
```

The Rust runners are built per platform and attached to the release separately, because they have to be compiled for the target.
That covers the text runners for Tantivy and SeekStorm and the vector runners for the exact scan, hnsw and turbovec.

## License

MIT.
