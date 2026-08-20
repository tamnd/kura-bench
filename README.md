# kura-bench

A benchmark suite for search, vector and graph engines, built to produce numbers that survive being argued with.

Every engine here loads the same data in the same order, is asked the same questions, and is measured by the operating system rather than by its own stopwatch.
Nothing is simulated and nothing is generated.
The corpus is real source trees, the vectors are the published SIFT and GIST descriptors with the ground truth that came with them, the graphs are the SNAP collaboration and web crawl networks, the queries are the sort of thing people actually type, and every figure in a report came from a process that really did the work.

There are three suites.
`kura-bench` measures full text engines, `kura-vecbench` measures vector indexes and `kura-graphbench` measures graph stores.
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

Relevance, where the corpus came with judgments.
Every result carries the page each query returned and not only how many documents matched, so the answers can be scored against what people judged relevant.
nDCG at 10, MRR at 10, recall at the depth the page was, success at 1, and the share of returned documents anybody judged either way.
Success at 1 is there because most enterprise search traffic is somebody who already knows which document they want, and for that person a relevant result at rank two is a failure that nDCG mostly forgives.
The definitions are the ones trec_eval uses and there is a test that checks them against a run of trec_eval itself, because a relevance score that cannot be lined up against a published one is a score that only compares against itself.
Every run also writes a file in the format trec_eval reads, so anybody who does not trust the arithmetic here can check it with the program everyone else uses.
This runs on the passage collection, which arrives with a real query log and a judgment file, and it does not run on the source checkouts, because nobody has judged those queries and inventing judgments to have a number would be worse than having none.

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
A runner that refuses still keeps its row in the report, which says it declined and why, because an engine that was asked and would not answer is a different thing from one nobody thought to measure.

## What is measured, in the graph suite

Everything the other two measure that still applies: load time and its parallelism, store size on disk against the size of the raw edge list, cold start in a second process, per operation latency, and throughput with several in flight.

What is different is that a graph store does not do one thing, it does five, and they have almost nothing in common.
A neighbour lookup is a single row read.
A two hop lookup is where the cost of a hub shows up.
A shortest path between two nodes that are not connected costs a full traversal of everything the start can reach.
A breadth first search touches everything and no index can help it.
PageRank walks every edge twenty times over and is an analytics job rather than a serving one.
So there is a table per operation rather than one number per engine, and a store that is quick at the first and hopeless at the last is visible as exactly that.

Correctness is checked the way recall is checked in the vector suite.
The answers are worked out once, in Go, by walking the same edge list the plainest way there is, and every runner is a separate implementation in another language.
Two independent implementations agreeing is real evidence, and one implementation agreeing with itself is not.
Every operation reduces to a list of whole numbers so a disagreement is a comparison rather than a schema.
A store that dropped half the edges answers every question faster than one that did not, and no timing column would ever say so, which is why the correctness table comes before the timings and why continuous integration fails on a disagreement instead of printing one.

## Engines

Text:

| Engine | Language | What it is |
| --- | --- | --- |
| bleve | Go | The scorch index, the usual answer for full text search inside a Go binary |
| sqlitefts | Go | SQLite FTS5 through a pure Go driver, the thing most projects reach for first |
| tantivy | Rust | A fast Lucene style index, the number worth being measured against |
| seekstorm | Rust | A newer memory mapped index making strong latency claims, worth checking |
| lucene | Java | What most enterprise search is actually running, since Elasticsearch and OpenSearch are this with a cluster around it |
| genba | Go | Our own index, the reason the rest of this exists |

Vector:

| Engine | Language | Metrics | What it is |
| --- | --- | --- | --- |
| exact | Rust | all three | The brute force scan, the thing every approximate index is measured against |
| hnsw | Rust | euclidean, cosine | The graph index, at the connection and construction settings hnswlib has defaulted to for years |
| turbovec | Rust | inner product | A quantizing index, one index per bit width because the width is fixed when it is built |

Graph:

| Engine | Language | What it is |
| --- | --- | --- |
| csr | Rust | A compressed sparse row adjacency and nothing else, the layout everything else is trying to beat |
| petgraph | Rust | The library a Rust program reaches for, driven through its adjacency rather than its algorithm module |
| sqlite | Go | Two integer columns and a covering index, the design a lot of software already has |
| ladybug | C++ | A property graph database, queried in Cypher, the only engine here that is one |

ladybug is the one engine that needs a step of its own, because it is a C++ shared library rather than a Go module or a crate.
`third_party/ladybug/fetch.sh` downloads the release its pin names and checks the archive against the sha256 in `third_party/ladybug/ladybug.json`, and `make ladybug` builds the runner against it.
Everything else in the suite builds and runs without it, and a machine that skips it gets a graph table with three rows instead of four rather than a failure.

    make ladybug

lucene needs a step of its own for the same reason, since it is a set of jars and a virtual machine rather than a module or a crate.
`runners/java/lucene/fetch.sh` downloads the jars its pin names and checks each one against the sha256 in `runners/java/lucene/lucene.json`, and `make lucene` compiles the runner against them and writes the wrapper the orchestrator runs.
A machine with no Java compiler gets a text table without that row rather than a failure.

    make lucene

Lucene 10 reads its memory mapped files through the foreign memory API, which is final from Java 22 and a preview before it, so an older virtual machine falls back to a slower path.
Build it on 22 or later, or the number in the table is not the one people are getting.

Each engine is a separate binary that speaks a small contract: create, load, flush, open, ask, close.
Adding one is a hundred lines and does not touch the harness.
The Go runners live under `runners/`, one directory each, the Rust runners share a cargo workspace at `runners/rust`, and the Java one is at `runners/java/lucene`, each language with one copy of the measuring code so that the same stopwatch times every engine written in it.
A text runner is called `<engine>-runner`, a vector runner `<engine>-vecrunner` and a graph runner `<engine>-graphrunner`, all built into `bin/`, and each suite only ever picks up its own.

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
Genba is the exception and says so in its own results: it drops stopwords from a query before it runs, so on a query containing one it matches fewer documents than the rest and its latency for that query is the cost of a smaller search.
Every engine stores the document body, so the index size comparison is like for like.

## Running it

```sh
make build
```

That builds all three orchestrators, the corpus builder, the two dataset fetchers and every runner into `bin/`.
The Rust runners are skipped with a message if there is no cargo on the machine, and the report says which engines ran.

Build the corpus:

```sh
bin/kura-corpus -src ~/corpus-src -out corpus.jsonl
```

That fetches six released projects at the commits pinned in `corpus/sources.go` and writes one JSON lines file from them.
The projects are Go, Kubernetes, Rust, PostgreSQL, Redis and Lucene, chosen for spread rather than for size: two large Go trees with a lot of generated code, a very large number of very small Rust test files, two C trees with long comment blocks that read like prose, and the source of a search engine.
The commit is pinned rather than the branch because a corpus is only a measuring instrument if it is the same instrument on every machine, and the main branch of six projects is a different set of files every day.
Each one is a shallow fetch of a single commit, the checkout is verified against the pin afterwards, and a checkout already sitting on the right commit is left alone, so running it again is cheap.

Building it is the slowest step in the repository and it only has to happen once per machine.

Source code is one shape of text and it is not the only one an engine gets asked to hold.
Three published corpora cover the other shapes, and each is downloaded rather than cloned:

```sh
bin/kura-corpus -datasets
bin/kura-corpus -dataset enron -out enron.jsonl
bin/kura-corpus -dataset msmarco -out msmarco.jsonl
bin/kura-corpus -dataset simplewiki -out simplewiki.jsonl
```

The mail archive is half a million short documents with real threads, real quoting and the same message sitting in a dozen mailboxes, which is the duplication every real deployment has and no generated corpus does.
The passage collection is nine million documents of a few dozen words each, which is the shape that stops a scorer's per document cost hiding behind its per posting cost, and it is the only one of these that arrives with real queries and the judgments to say which passage answered them.
The encyclopaedia dump is long articles with the wiki markup left on, because an engine in front of a wiki is handed the wiki's own markup and a corpus that strips it measures something nobody runs.

The download is checksummed against a pin in `corpus/datasets.go` for the same reason the source trees pin a commit, and an archive already on the machine with the right checksum is left alone.
`-limit` takes the first n documents, which is useful on a machine without room for the whole thing and which makes the file a latency corpus rather than a relevance corpus, since a judged document past the cut cannot be retrieved.
`kura-corpus -datasets` prints the licence position for each one, and one of the three is local use only.

That last point is enforced rather than remembered.
Every built corpus gets a small `<corpus>.dataset.json` beside it saying what it is and whether content from it may leave the machine, and `kura-bench` reads it before anything runs.
For a corpus that may not, the result files carry the timings, the hit counts, the sizes and the query text, and not the list of documents each engine returned.
That list is the one field in a result file that can identify a person, since the mail corpus names its documents by mailbox path, and a result file is exactly the kind of thing somebody commits without thinking about it.
A corpus with no label beside it is treated as restricted and says so on stderr, which is the wrong answer for most corpora and the right default anyway: being wrong that way costs a rerun, and being wrong the other way puts a real person's name in a public history, which no later commit undoes.

For trying something out there are two other forms, which do not produce a comparable result and are not meant to:

```sh
bin/kura-corpus -repo linux=/src/linux -repo llvm=/src/llvm-project -out corpus.jsonl
bin/kura-corpus -root ~/corpus -out corpus.jsonl
```

Then run everything:

```sh
bin/kura-bench -corpus corpus.jsonl -queries queries.txt -bin bin -out results
```

This writes `results/<engine>-<host>.json` for each engine and `results/report-<host>.md` alongside them.
The JSON is the record, the markdown is what a person reads.

Useful flags:

- `-engines bleve,genba` runs a subset instead of everything found in `-bin`, in the order it names them, so an engine that takes hours to index can be put last instead of holding up the results you are actually waiting on. Without the flag the order is alphabetical. Naming an engine that has no runner fails immediately rather than after the rest of the run.
- `-limit 200000` stops after that many documents, which is how a shared machine gets a run that finishes.
- `-repeat 50` runs each query that many times, the default is twenty.
- `-workers 16` sets the concurrency for the several-in-flight phase, the default is the core count.
- `-deadline 30m` gives up on a phase that runs longer than that. The engine stays in the report with whatever the phases that did finish measured, and the tables it has no numbers for say it ran out of time. There is no limit by default, because a slow number is worth having and a missing row is not, and it exists for the other case: an engine whose query cost is proportional to the match set does not stop, it just keeps going, and one of those will hold a whole run for as long as you let it.
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

- `-metric euclidean|cosine|inner-product` picks what nearest means, and an engine that cannot answer that question declines the run instead of reporting a meaningless recall, which the report says next to its name.
- `-engines exact,hnsw` runs a subset.
- `-k 10` is how many neighbours are asked for and the depth recall is scored at.
- `-limit 100000` indexes part of the base set, which is how a shared machine gets a run that finishes. Recall then becomes a lower bound, because the ground truth still covers the whole set, and the report says so.
- `-queries 1000` uses part of the query set.
- `-deadline 30m` gives up on a phase that runs longer than that. The engine stays in the report with whatever the phases that did finish measured, and the tables it has no numbers for say it ran out of time. There is no limit by default, because a slow number is worth having and a missing row is not, and it exists for the other case: an engine whose query cost is proportional to the match set does not stop, it just keeps going, and one of those will hold a whole run for as long as you let it.

## Running the graph suite

Fetch a graph.
The addresses, the node and edge counts and the checksums are pinned in `graphs/dataset.go`, and the fetcher checks its own parse against the counts the publisher printed before it writes anything:

```sh
bin/kura-graphs -dataset ca-grqc -out graphdata
```

The same command works out the answers every runner is scored against and writes them next to the edges.
`ca-grqc` is 29 thousand edges and is what continuous integration runs, `web-google` is 5 million and `soc-livejournal` is 69 million.
Working out the answers on the large two takes a while and it only has to happen once per machine.

A machine that cannot hold the whole of a large graph prepares a smaller one instead of loading part of a big one:

```sh
bin/kura-graphs -dataset soc-livejournal -nodes 500000 -out graphdata
```

That keeps the 500 thousand lowest identifiers and every edge with both ends among them, writes it to `graphdata/soc-livejournal-n500000`, and works out that graph's own answers.
The text and vector suites have a `-limit` for this and the graph suite does not, because the two situations are not the same.
Indexing half a corpus still answers a query correctly about the half it has, and recall against the full ground truth is then a lower bound worth reading.
Cutting an edge list in half instead leaves nodes that have lost most of their neighbours, so every traversal answer is wrong by an amount nothing can measure, and a correctness column on it would be reporting noise.
A subgraph is a real graph, so the answers are the right answers and the numbers mean what they say.
What it is not is a sample of the original, and the report is labelled with the subgraph's name so that a result on it is never mistaken for a result on the whole thing.

Then run the engines:

```sh
bin/kura-graphbench -dataset ca-grqc -data graphdata -bin bin -out results
```

This writes `results/graph-<engine>-<dataset>-<host>.json` and `results/graph-report-<dataset>-<host>.md`.

Useful flags:

- `-engines csr,sqlite` runs a subset.
- `-ops neighbours,bfs` runs a subset of the operations, in report order whatever order they are given in.
- `-graph graphdata/soc-livejournal-n500000` runs against a prepared directory rather than a named dataset, which is how a subgraph is measured.
- `-workers 16` sets the concurrency for the several-in-flight phase, the default is the core count.
- `-deadline 30m` gives up on a phase that runs longer than that. The engine stays in the report with whatever the phases that did finish measured, and the tables it has no numbers for say it ran out of time. There is no limit by default, because a slow number is worth having and a missing row is not, and it exists for the other case: an engine whose query cost is proportional to the match set does not stop, it just keeps going, and one of those will hold a whole run for as long as you let it.
- `-keep` leaves the built stores in place instead of deleting them.

## Rebuilding a report

Each orchestrator writes its report at the end of its own run, from the engines that run asked for.
That is fine while a run is happening and wrong afterwards, because rerunning one engine, which is the normal thing to do when a pin moves or a bug is fixed, overwrites the machine's report with a table of one row.
The result files are untouched, so nothing is actually lost, but the document a person reads is now missing every rival.

```sh
make report
```

That reads every result file in `results`, groups them by suite, dataset and machine, and writes each report from everything that machine has produced.
It runs no engines and opens no network connections, so it is safe to run on a laptop against results collected on a server.

## The query set

`queries.txt` is grouped by intent, because the interesting thing is not the average, it is where the engines disagree.
There are very common terms that match most of the corpus, ordinary two and three word queries, rare identifiers that match a handful of documents, prose terms that only appear in documentation, and one worst case single word.
An engine that is quick on rare terms and slow on common ones has a different problem from one that is uniformly slow, and an average would hide both.

Two lines of it are not comparable across engines and are kept anyway.
`mmap_region` and `tsan_atomic` are split on the underscore by every engine here when it indexes, so they all hold the same documents, but they disagree about what the query means: some read the two halves as an OR and match thousands of documents, and some read them as a phrase and match none.
That is a real disagreement about a real query shape, since internal jargon and product codenames are written with underscores and people type them into search boxes.
The latency on those two lines is two different questions being answered rather than one engine beating another, and the file says so where somebody reading the query set will see it.

That file is about source code, because the checkouts are.
Running it against mail or an encyclopaedia would measure a dictionary miss and call it a search, so each downloaded corpus gets its own set from `kura-queries`.

```
bin/kura-queries -corpus enron.jsonl -out queries-enron.txt
bin/kura-queries -log queries.dev.small.tsv -n 40 -out queries-msmarco.txt
```

`-log` is the honest one and is used wherever a real log came down with the corpus, which for the passage collection means real search queries typed by real people.
`-corpus` is for the corpora that arrive without one.
It reads the corpus, counts how many documents each term is in, and picks from four bands of document frequency, because document frequency is what decides what a query costs an engine.
Terms tied on frequency are walked at a stride rather than taken adjacently, so a band does not come out as three words that happen to sit next to each other in the dictionary.
It also picks a few two word queries from adjacent pairs where both halves are common, since a query that is cheap word by word and expensive together is the case an engine either skips through or does not.
The header of a generated file says it was constructed rather than logged, because a constructed query set tells you about latency and nothing about relevance.

## Scoring the answers

A latency number says how fast an engine answered.
It does not say whether it answered.
An engine that returns the wrong ten documents in a tenth of the time has not won anything, and a table of medians is the easiest place in the world to hide that.

```
make textset TEXTSET=msmarco
make textbench TEXTSET=msmarco
bin/kura-relevance -results results -runs runs -out scores.json
```

The passage collection is the corpus that makes this possible, because it comes with a query log and a judgment file rather than only documents.
The runners report the identifiers of the page they returned alongside the timings, collected on the warm up run so that no timed run pays for it, and the scorer matches those against the judgments.

Two things it prints are worth reading before the scores.
Recall is at the depth of the page, which is ten by default, and not the hundred a paper quotes, so it is a much harder number and the two are not comparable.
Recall at a hundred is the one that says whether a first stage retriever gave a reranker anything to work with, since a document the first stage missed cannot be recovered by anything downstream, and to get it the query phase has to be rerun asking for a deeper page.

```
make textbench TEXTSET=msmarco DEPTH=100
make relevance DEPTH=100
```

The depth is a flag on the whole run rather than a deeper untimed probe alongside shallow timed runs, because the second arrangement warms caches that the timed runs then benefit from and the bias would not show up anywhere in the output.
So a run at a hundred has honest recall and latencies that belong to a page of a hundred, and the report says so above the search table rather than leaving it in a JSON field.
The scorer refuses to quietly compute recall at a depth the engines were never asked to fill, and says which ones came up short.
Judged coverage is the share of returned documents anybody looked at, and when it is low the scores are mostly measuring how much an engine agrees with the systems that were in the pool when the judgments were made, rather than how good it is.

`-runs` writes a run file per engine in the format every existing evaluation tool reads.
That is there so somebody who does not believe these numbers can check them with a different program, which is the property that makes them worth reporting at all.

`-out` writes the scores as JSON, and `-baseline` checks a fresh scores file against an earlier one and exits nonzero when nDCG has fallen by more than the tolerance.

```
bin/kura-relevance -results results -out scores.json -baseline baseline/msmarco.json
```

The tolerance lives in the baseline file rather than on the command line, because it describes how much these particular numbers move between two runs that changed nothing, and the only way to know that is to run the same benchmark several times and look at the spread.
A baseline with no tolerance in it is refused rather than treated as a demand for identical scores, since a gate that fails on noise is a gate people learn to ignore.
There is no baseline committed here yet, because measuring that spread needs the passage collection to have run more than once on one machine, and until it has, any number put in that field would be a guess wearing the clothes of a measurement.

## Reading a result

Two things are worth knowing before comparing numbers from two files.

Peak resident memory is a process wide monotone maximum, so a per phase peak is not recoverable from it.
What is reported is the peak as it stood at the end of each phase, and the report says that.

Some engines have no on disk form.
The genba runner today uses an in memory store, so its open phase is a full reindex from the corpus.
That is the honest cold start for an in memory store rather than a favour done to it, and it is written down in the result notes rather than left for somebody to work out.

Each result carries a `run` block holding the SHA-256 of the corpus and of the query file, the commit the orchestrator was built from, when the run started, and every parameter it was given.
The point is that somebody who wants to argue with a number can start from the same bytes and the same code rather than from something that happens to have the same name.
Two files whose corpus digests differ are not a comparison no matter how similar the tables look, and the commit is read out of the build information rather than by asking git, because the binary that produced the numbers is the fact worth recording and the checkout beside it may have moved on.
A binary built from a modified tree says so, since a run from an uncommitted tree cannot be reproduced from the commit alone.

## Continuous integration

Every pull request builds and tests on Linux and macOS with the race detector on, cross compiles for Windows, Linux arm64 and macOS arm64, builds and clippies the Rust crate on all three platforms, and then does a real end to end run.
The smoke run builds a corpus out of this repository's own checkout and runs every engine over it.
It is a small corpus and the numbers from it are not worth publishing, but it exercises the corpus builder, every runner, both phases, the merge and the report.
A harness that only ever ran on a developer's laptop is a harness that has silently stopped working.

The vector suite gets the same treatment on siftsmall, under both Euclidean and inner product.
That run asserts one figure rather than printing it: the exact scan has to score exactly one against the published ground truth, and the job fails if it does not.
It is the only number in the suite with a known right answer, so it is the one worth checking on every pull request.

The graph suite gets the same treatment on ca-GrQc.
That run fetches the graph, works out the answers, runs every engine and then fails if any of them disagrees with the reference on any operation.
Three implementations across two languages landing on the same numbers is the check, and a run where they do not is a bug report rather than a benchmark.

## Installing

Every tagged release publishes archives for Linux, macOS and Windows on amd64 and arm64, deb, rpm and apk packages, and a container image on GHCR.
The archive contains all three orchestrators, the corpus builder, the two dataset fetchers, every Go runner and the query set, so unpacking it on a machine is the whole setup.

```sh
docker run --rm -v "$PWD:/bench" ghcr.io/tamnd/kura-bench:latest \
  -corpus /bench/corpus.jsonl -queries /bench/queries.txt -bin /usr/bin -out /bench/results
```

The Rust runners are built per platform and attached to the release separately, because they have to be compiled for the target.
That covers the text runners for Tantivy and SeekStorm, the vector runners for the exact scan, hnsw and turbovec, and the graph runners for the sparse row adjacency and petgraph.
The ladybug runner is not in the archive either, since it links against a library that has to be fetched for the target first.

## License

MIT.
