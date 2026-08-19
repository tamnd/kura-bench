# Vector results on vmi3112167

Dataset siftsmall, ranked by cosine similarity, 10 neighbours per query, 100 queries.

There is no published cosine ground truth for this dataset, so it was computed here with a full exact scan and cached.
The exact scan therefore scores one by construction and is not evidence of anything, unlike in a Euclidean run.

## Machine

vmi3112167, linux/x86_64, AMD EPYC Processor (with IBPB), 6 cores, 11.68 GB of memory.
Load before the run was 11.62 and 6.82 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Dataset

siftsmall, 10,000 vectors of 128 components, 100 queries, recall at 10, ranked by cosine similarity.
The vectors are 4.9 MB as plain float32, which is what the index sizes below are measured against.

## Throughput at a fixed accuracy

| engine | version | at recall 0.90 | settings | at recall 0.99 | settings |
| --- | --- | --- | --- | --- | --- |
| exact | 0.1.0 | 1,578 q/s |   | 1,578 q/s |   |
| hnsw | 0.3.4 | 3,769 q/s | ef=16 | 778 q/s | ef=512 |
| turbovec | 1.0.0 | declined | | | |

Each cell is the fastest setting that reached that recall, so the engines are compared at the same accuracy rather than at their own favourite settings.
An empty cell means no setting the runner tried got there.

## Building the index

| engine | wall | vectors/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- |
| exact | 55 ms | 182,764 | 0.0 | 0.6x | 11.8 MB |
| hnsw | 10.5 s | 952 | 24.7 | 2.4x | 33.0 MB |

Build time is the cost of changing your mind about an index, and on a graph index it is the largest number in this report.

## Storage

| engine | index on disk | files | bytes per vector | index over raw vectors |
| --- | --- | --- | --- | --- |
| exact | 4.9 MB | 1 | 512 | 1.00x |
| hnsw | 10.0 MB | 2 | 1053 | 2.06x |

Below one means the engine is not keeping the full precision vectors, which is the whole point of a quantizing index and is also where its recall ceiling comes from.
Above one is the cost of a graph, which buys the search time back.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| exact | 22 ms | 0.02 | 7.3 MB |
| hnsw | 442 ms | 0.24 | 36.3 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Recall against speed

| engine | settings | recall | median | p99 | queries/s | measured |
| --- | --- | --- | --- | --- | --- | --- |
| exact |   | 1.0000 | 1.33 ms | 3.22 ms | 1,578 | 6 in flight |
| hnsw | ef=16 | 0.9470 | 0.20 ms | 3.41 ms | 3,769 | 6 in flight |
| hnsw | ef=32 | 0.9740 | 0.30 ms | 0.59 ms | 3,484 | 6 in flight |
| hnsw | ef=64 | 0.9810 | 0.65 ms | 12 ms | 1,679 | 6 in flight |
| hnsw | ef=128 | 0.9870 | 1.15 ms | 14 ms | 1,486 | 6 in flight |
| hnsw | ef=256 | 0.9890 | 2.26 ms | 7.07 ms | 563 | 6 in flight |
| hnsw | ef=512 | 0.9900 | 3.36 ms | 24 ms | 778 | 6 in flight |
| turbovec | | declined | | | | |

Recall is against the exact ground truth for this metric, at the k in the dataset line above.
Latency is one query at a time and throughput is with several in flight, because a server does one and a batch job does the other.

## Notes

- hnsw: built with 16 connections per node and an ef of 200, which are the defaults the library is usually run at rather than settings picked for this run
- turbovec: this engine ranks by inner-product, and the run asked for cosine, so it has no numbers here rather than having been left out of the run.

