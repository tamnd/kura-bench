# Results on USERnoMacBook-Air.local

Corpus corpus.jsonl, queries queries.txt, 20 timed runs per query.

## Machine

USERnoMacBook-Air.local, macos/aarch64, Apple M4, 10 cores, 24.00 GB of memory.
Load before the run was 5.79, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Machine load per phase

| engine | indexing, start to end | search, start to end |
| --- | --- | --- |
| sqlite-fts5 | 39.12 to 55.57 | 55.57 to 54.52 |

sqlite-fts5 were measured while the machine was busy with other work, so their numbers are a floor rather than a measurement.
The engines beside it were not, so the gap between them is partly the machine.

## Corpus

82,789 documents, 464.0 MB of text.

## Indexing

| engine | version | wall | docs/s | MB/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| kura | 0.1.0 | 6.9 s | 11,973 | 67.1 | 14.2 | 2.1x | 478.0 MB |
| seekstorm | 3.3.5 | 1m05s | 1,261 | 7.1 | 81.0 | 1.2x | 3.36 GB |
| sqlite-fts5 | v1.57.0 | 50.3 s | 1,647 | 9.2 | 32.1 | 0.6x | 69.9 MB |
| tantivy | tantivy v0.26.1, index_format v7 | 7.0 s | 11,827 | 66.3 | 16.7 | 2.4x | 454.4 MB |

## Storage

| engine | index on disk | files | index over corpus | bytes written |
| --- | --- | --- | --- | --- |
| kura | 202.1 MB | 1 | 0.44x | none |
| seekstorm | 349.6 MB | 73 | 0.75x | none |
| sqlite-fts5 | 650.6 MB | 3 | 1.40x | none |
| tantivy | 279.8 MB | 52 | 0.60x | none |

Index over corpus below one means the engine does not keep the document text.
Bytes written is what the process asked the kernel to write, which on some platforms counts every handle and not only files.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| kura | 2 ms | 0.00 | 3.7 MB |
| seekstorm | 37 ms | 0.16 | 383.4 MB |
| sqlite-fts5 | 41 ms | 0.02 | 2.7 MB |
| tantivy | 5 ms | 0.00 | 8.6 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Search, one query at a time

| engine | median | p90 | p99 | CPU ms per query | peak RSS |
| --- | --- | --- | --- | --- | --- |
| kura | 0.16 ms | 0.57 ms | 0.81 ms | 0.3 | 9.4 MB |
| seekstorm | 0.35 ms | 1.36 ms | 1.92 ms | 0.9 | 409.5 MB |
| sqlite-fts5 | 11 ms | 252 ms | 517 ms | 47.9 | 20.4 MB |
| tantivy | 0.19 ms | 0.65 ms | 1.26 ms | 0.4 | 23.3 MB |

## What the query set read

| engine | index | resident before | faulted in | of that, from storage | share of index read |
| --- | --- | --- | --- | --- | --- |
| kura | 186.0 MB | 100% | 5.7 MB | none | 3.1% |

Faulted in is a floor, because one fault can bring in more than one page.
An index that was already resident and faulted nothing from storage means the latencies above were answered out of memory, which is the best case and not the only case.

## Search, several in flight

| engine | workers | queries/s | median | p99 |
| --- | --- | --- | --- | --- |
| kura | 13 | 18,753 | 0.23 ms | 1.88 ms |
| seekstorm | 13 | 8,802 | 1.25 ms | 3.67 ms |
| sqlite-fts5 | 13 | 52 | 68 ms | 865 ms |
| tantivy | 13 | 14,938 | 0.28 ms | 5.13 ms |

## Incremental update

| engine | documents | wall | docs/s | index after | growth |
| --- | --- | --- | --- | --- | --- |
| kura | 5,000 | 415 ms | 12,039 | 222.7 MB | +10.2% |
| seekstorm | 5,000 | 18.7 s | 267 | 380.5 MB | +8.8% |
| sqlite-fts5 | 5,000 | 7.2 s | 698 | 704.3 MB | +8.3% |
| tantivy | 5,000 | 1.7 s | 2,891 | 306.8 MB | +9.6% |

Growth is what rewriting documents the index already had cost in space.
An engine that never reclaims the old copies grows by roughly the size of the update.

## Per query

| query | engine | hits | median | p99 |
| --- | --- | --- | --- | --- |
| memory allocation | kura | 4,751 | 0.16 ms | 0.19 ms |
| memory allocation | seekstorm | 4,315 | 0.72 ms | 1.92 ms |
| memory allocation | sqlite-fts5 | 4,753 | 17 ms | 157 ms |
| memory allocation | tantivy | 4,753 | 0.20 ms | 0.22 ms |
| return value | kura | 32,997 | 0.55 ms | 0.60 ms |
| return value | seekstorm | 32,158 | 0.96 ms | 1.37 ms |
| return value | sqlite-fts5 | 32,997 | 149 ms | 288 ms |
| return value | tantivy | 32,997 | 0.65 ms | 0.71 ms |
| error handling | kura | 31,008 | 0.25 ms | 0.29 ms |
| error handling | seekstorm | 29,933 | 0.22 ms | 0.30 ms |
| error handling | sqlite-fts5 | 31,008 | 112 ms | 248 ms |
| error handling | tantivy | 31,008 | 0.53 ms | 0.56 ms |
| deadlock detection | kura | 804 | 0.22 ms | 0.67 ms |
| deadlock detection | seekstorm | 752 | 0.43 ms | 0.67 ms |
| deadlock detection | sqlite-fts5 | 804 | 2.05 ms | 3.52 ms |
| deadlock detection | tantivy | 804 | 0.10 ms | 0.14 ms |
| reference counting | kura | 4,317 | 0.11 ms | 0.21 ms |
| reference counting | seekstorm | 4,058 | 0.22 ms | 0.30 ms |
| reference counting | sqlite-fts5 | 4,317 | 11 ms | 40 ms |
| reference counting | tantivy | 4,317 | 0.16 ms | 0.19 ms |
| buffer overflow check | kura | 19,602 | 0.34 ms | 0.53 ms |
| buffer overflow check | seekstorm | 13,443 | 1.14 ms | 1.49 ms |
| buffer overflow check | sqlite-fts5 | 19,602 | 49 ms | 79 ms |
| buffer overflow check | tantivy | 19,602 | 0.46 ms | 0.53 ms |
| thread pool shutdown | kura | 3,761 | 0.12 ms | 0.16 ms |
| thread pool shutdown | seekstorm | 3,180 | 0.31 ms | 0.39 ms |
| thread pool shutdown | sqlite-fts5 | 3,763 | 9.65 ms | 14 ms |
| thread pool shutdown | tantivy | 3,763 | 0.19 ms | 0.26 ms |
| mmap_region | kura | 1,371 | 0.05 ms | 0.07 ms |
| mmap_region | seekstorm | 0 | 0.03 ms | 0.08 ms |
| mmap_region | sqlite-fts5 | 1,373 | 5.28 ms | 15 ms |
| mmap_region | tantivy | 0 | 0.04 ms | 0.07 ms |
| kasan | kura | 9 | 0.05 ms | 0.08 ms |
| kasan | seekstorm | 3 | 0.17 ms | 0.77 ms |
| kasan | sqlite-fts5 | 9 | 0.15 ms | 2.81 ms |
| kasan | tantivy | 9 | 0.04 ms | 0.05 ms |
| tsan_atomic | kura | 2,394 | 0.06 ms | 0.09 ms |
| tsan_atomic | seekstorm | 0 | 0.03 ms | 0.06 ms |
| tsan_atomic | sqlite-fts5 | 2,394 | 8.33 ms | 247 ms |
| tsan_atomic | tantivy | 0 | 0.03 ms | 0.04 ms |
| backwards compatibility guarantee | kura | 2,472 | 0.07 ms | 0.10 ms |
| backwards compatibility guarantee | seekstorm | 2,231 | 0.35 ms | 0.53 ms |
| backwards compatibility guarantee | sqlite-fts5 | 2,473 | 9.79 ms | 149 ms |
| backwards compatibility guarantee | tantivy | 2,473 | 0.15 ms | 0.19 ms |
| deprecated in favour of | kura | 53,301 | 0.75 ms | 0.81 ms |
| deprecated in favour of | seekstorm | 51,164 | 0.41 ms | 0.58 ms |
| deprecated in favour of | sqlite-fts5 | 53,302 | 300 ms | 517 ms |
| deprecated in favour of | tantivy | 53,301 | 1.19 ms | 1.26 ms |
| the | kura | 51,086 | 0.45 ms | 0.50 ms |
| the | seekstorm | 50,989 | 1.12 ms | 1.71 ms |
| the | sqlite-fts5 | 51,086 | 160 ms | 298 ms |
| the | tantivy | 51,086 | 0.44 ms | 0.46 ms |

Two engines that disagree about the hit count are not answering the same question, and their latencies are not comparable.

