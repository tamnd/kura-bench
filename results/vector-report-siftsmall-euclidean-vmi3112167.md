# Vector results on vmi3112167

Dataset siftsmall, ranked by Euclidean distance, 10 neighbours per query, 100 queries.

The ground truth is the one published with the dataset, so the exact scan's recall is a real check on this suite: anything other than one means the files are being read wrongly and every figure below is wrong the same way.

## Machine

vmi3112167, linux/x86_64, AMD EPYC Processor (with IBPB), 6 cores, 11.68 GB of memory.
Load before the run was 10.74 and 6.85 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Dataset

siftsmall, 10,000 vectors of 128 components, 100 queries, recall at 10, ranked by Euclidean distance.
The vectors are 4.9 MB as plain float32, which is what the index sizes below are measured against.

## Throughput at a fixed accuracy

| engine | version | at recall 0.90 | settings | at recall 0.99 | settings |
| --- | --- | --- | --- | --- | --- |
| exact | 0.1.0 | 1,950 q/s |   | 1,950 q/s |   |
| hnsw | 0.3.4 | 4,861 q/s | ef=32 | not reached |   |
| turbovec | 1.0.0 | declined | | | |

Each cell is the fastest setting that reached that recall, so the engines are compared at the same accuracy rather than at their own favourite settings.
An empty cell means no setting the runner tried got there.

## Building the index

| engine | wall | vectors/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- |
| exact | 75 ms | 132,483 | 0.1 | 1.0x | 11.9 MB |
| hnsw | 15.5 s | 645 | 21.6 | 1.4x | 33.7 MB |

Build time is the cost of changing your mind about an index, and on a graph index it is the largest number in this report.

## Storage

| engine | index on disk | files | bytes per vector | index over raw vectors |
| --- | --- | --- | --- | --- |
| exact | 4.9 MB | 1 | 512 | 1.00x |
| hnsw | 10.1 MB | 2 | 1061 | 2.07x |

Below one means the engine is not keeping the full precision vectors, which is the whole point of a quantizing index and is also where its recall ceiling comes from.
Above one is the cost of a graph, which buys the search time back.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| exact | 17 ms | 0.01 | 7.2 MB |
| hnsw | 301 ms | 0.28 | 36.9 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Recall against speed

| engine | settings | recall | median | p99 | queries/s | measured |
| --- | --- | --- | --- | --- | --- | --- |
| exact |   | 1.0000 | 1.05 ms | 4.01 ms | 1,950 | 6 in flight |
| hnsw | ef=16 | 0.9370 | 0.17 ms | 0.28 ms | 4,077 | 6 in flight |
| hnsw | ef=32 | 0.9620 | 0.39 ms | 6.50 ms | 4,861 | 6 in flight |
| hnsw | ef=64 | 0.9740 | 0.48 ms | 6.11 ms | 4,031 | 6 in flight |
| hnsw | ef=128 | 0.9760 | 0.90 ms | 23 ms | 2,069 | 6 in flight |
| hnsw | ef=256 | 0.9810 | 5.28 ms | 34 ms | 788 | 6 in flight |
| hnsw | ef=512 | 0.9850 | 2.17 ms | 3.15 ms | 794 | 6 in flight |
| turbovec | | declined | | | | |

Recall is against the exact ground truth for this metric, at the k in the dataset line above.
Latency is one query at a time and throughput is with several in flight, because a server does one and a batch job does the other.

## Notes

- hnsw: built with 16 connections per node and an ef of 200, which are the defaults the library is usually run at rather than settings picked for this run
- turbovec: this engine ranks by inner-product, and the run asked for euclidean, so it has no numbers here rather than having been left out of the run.

