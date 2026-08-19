# kura-bench

A benchmark suite for search engines, built to produce numbers that survive being argued with.

Every engine here indexes the same documents in the same order, is searched with the same queries, and is measured by the operating system rather than by its own stopwatch.
Nothing is simulated and nothing is generated.
The corpus is real source trees, the queries are the sort of thing people actually type, and every figure in a report came from a process that really did the work.

## Why it exists

The first version of this was not a repository at all, it was a script that pointed each engine at a directory of checkouts and timed it.
The numbers it produced were nonsense.
A first pass over a checkout on a Windows machine ran two orders of magnitude slower than a second pass over the same files, and no amount of staring at the engine explained the difference, because the difference was not in the engine.
It was the filesystem being touched for the first time.

So the corpus became one JSON lines file.
Reading it is sequential, it fits in the page cache, and the same file can be copied to another machine and produce a comparable result.
What is left after that is the engine.

## What is measured

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

## Engines

| Engine | Language | What it is |
| --- | --- | --- |
| bleve | Go | The scorch index, the usual answer for full text search inside a Go binary |
| sqlitefts | Go | SQLite FTS5 through a pure Go driver, the thing most projects reach for first |
| tantivy | Rust | A fast Lucene style index, the number worth being measured against |
| genba | Go | Our own index, the reason the rest of this exists |

Each engine is a separate binary that speaks a small contract: create, add a batch, flush, open, search, close.
Adding one is a hundred lines and does not touch the harness.

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

That builds the orchestrator, the corpus builder and every runner into `bin/`.
The Rust runner is skipped with a message if there is no cargo on the machine, and the report says which engines ran.

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

## Installing

Every tagged release publishes archives for Linux, macOS and Windows on amd64 and arm64, deb, rpm and apk packages, and a container image on GHCR.
The archive contains the orchestrator, the corpus builder, every Go runner and the query set, so unpacking it on a machine is the whole setup.

```sh
docker run --rm -v "$PWD:/bench" ghcr.io/tamnd/kura-bench:latest \
  -corpus /bench/corpus.jsonl -queries /bench/queries.txt -bin /usr/bin -out /bench/results
```

The Tantivy runner is built per platform and attached to the release separately, because it has to be compiled for the target.

## License

MIT.
