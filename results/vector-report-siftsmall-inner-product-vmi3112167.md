# Vector results on vmi3112167

Dataset siftsmall, ranked by maximum inner product, 10 neighbours per query, 100 queries.

There is no published inner-product ground truth for this dataset, so it was computed here with a full exact scan and cached.
The exact scan therefore scores one by construction and is not evidence of anything, unlike in a Euclidean run.

## Machine

vmi3112167, linux/x86_64, AMD EPYC Processor (with IBPB), 6 cores, 11.68 GB of memory.
Load before the run was 11.46 and 6.00 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Dataset

siftsmall, 10,000 vectors of 128 components, 100 queries, recall at 10, ranked by maximum inner product.
The vectors are 4.9 MB as plain float32, which is what the index sizes below are measured against.

## Throughput at a fixed accuracy

| engine | version | at recall 0.90 | settings | at recall 0.99 | settings |
| --- | --- | --- | --- | --- | --- |
| exact | 0.1.0 | 1,681 q/s |   | 1,681 q/s |   |
| hnsw | 0.3.4 | declined | | | |
| turbovec | 1.0.0 | not reached |   | not reached |   |

Each cell is the fastest setting that reached that recall, so the engines are compared at the same accuracy rather than at their own favourite settings.
An empty cell means no setting the runner tried got there.

## Building the index

| engine | wall | vectors/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- |
| exact | 37 ms | 272,256 | 0.0 | 0.8x | 11.8 MB |
| turbovec | 799 ms | 12,515 | 0.7 | 0.8x | 14.9 MB |

Build time is the cost of changing your mind about an index, and on a graph index it is the largest number in this report.

## Storage

| engine | index on disk | files | bytes per vector | index over raw vectors |
| --- | --- | --- | --- | --- |
| exact | 4.9 MB | 1 | 512 | 1.00x |
| turbovec | 2.0 MB | 4 | 215 | 0.42x |

Below one means the engine is not keeping the full precision vectors, which is the whole point of a quantizing index and is also where its recall ceiling comes from.
Above one is the cost of a graph, which buys the search time back.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| exact | 32 ms | 0.03 | 7.2 MB |
| turbovec | 420 ms | 0.40 | 4.1 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Recall against speed

| engine | settings | recall | median | p99 | queries/s | measured | index | build |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| exact |   | 1.0000 | 1.24 ms | 2.03 ms | 1,681 | 6 in flight |   |   |
| hnsw | | declined | | | | | | |
| turbovec | bits=2 | 0.7020 | 0.12 ms | 0.17 ms | 6,541 | 6 in flight | 446 KB | 226 ms |
| turbovec | bits=3 | 0.8550 | 0.24 ms | 1.29 ms | 6,194 | 6 in flight | 824 KB | 265 ms |
| turbovec | bits=4 | 0.8980 | 0.22 ms | 0.39 ms | 3,963 | 6 in flight | 824 KB | 287 ms |

Recall is against the exact ground truth for this metric, at the k in the dataset line above.
Latency is one query at a time and throughput is with several in flight, because a server does one and a batch job does the other.
The last two columns are filled in for an engine whose setting is fixed when the index is built, where each row is a different index rather than a different way of searching the same one.
For those engines the build and storage tables above cover the whole sweep together, since that is what the run actually cost.

## Notes

- hnsw: this engine ranks by euclidean or cosine, and the run asked for inner-product, so it has no numbers here rather than having been left out of the run.
- turbovec: the build figures cover all 3 indexes together, one per bit width, since that is what the run cost; the per width cost is in the curve; the engine was held to one thread so that a serial query is one query on one core, as it is for every other engine here; the cold start is the bits=2 index, the first of the three loaded

