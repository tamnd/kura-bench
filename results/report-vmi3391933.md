# Results on vmi3391933

20 timed runs per query.

## Machine

vmi3391933, linux/amd64, AMD EPYC Processor (with IBPB), 8 cores, 23.47 GB of memory.
Load before the run was 12.91 and 5.04 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Corpus

82,791 documents, 464.0 MB of text.

## Indexing

| engine | version | wall | docs/s | MB/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| genba | v0.0.0-20260819135620-17146945489f | 19m30s | 70 | 0.4 | 967.7 | 0.8x | 210.7 MB |
| seekstorm | 3.3.5 | 1m47s | 769 | 4.3 | 138.5 | 1.3x | 4.74 GB |
| sqlite-fts5 | v1.56.0 | 2m19s | 592 | 3.3 | 104.9 | 0.8x | 65.0 MB |
| tantivy | tantivy v0.26.1, index_format v7 | 21.6 s | 3,836 | 21.5 | 52.4 | 2.4x | 440.9 MB |

## Storage

| engine | index on disk | files | index over corpus | bytes written |
| --- | --- | --- | --- | --- |
| genba | 1.13 GB | 3 | 2.50x | 4.86 GB |
| seekstorm | 346.5 MB | 59 | 0.75x | 346.6 MB |
| sqlite-fts5 | 648.8 MB | 3 | 1.40x | 2.39 GB |
| tantivy | 279.4 MB | 40 | 0.60x | 535.7 MB |

Index over corpus below one means the engine does not keep the document text.
Bytes written is what the process asked the kernel to write, which on some platforms counts every handle and not only files.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| genba | 4.5 s | 1.28 | 35.3 MB |
| seekstorm | 761 ms | 0.92 | 194.8 MB |
| sqlite-fts5 | 91 ms | 0.07 | 12.8 MB |
| tantivy | 5 ms | 0.01 | 13.2 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Search, one query at a time

| engine | median | p90 | p99 | CPU ms per query | peak RSS |
| --- | --- | --- | --- | --- | --- |
| genba | 133 ms | 661 ms | 1.5 s | 298.7 | 127.5 MB |
| seekstorm | 0.86 ms | 13 ms | 162 ms | 3.2 | 214.4 MB |
| sqlite-fts5 | 39 ms | 454 ms | 636 ms | 121.3 | 15.9 MB |
| tantivy | 0.34 ms | 2.40 ms | 33 ms | 1.1 | 36.4 MB |

## Search, several in flight

| engine | workers | queries/s | median | p99 |
| --- | --- | --- | --- | --- |
| genba | 13 | 2 | 2.2 s | 16.6 s |
| seekstorm | 13 | 553 | 12 ms | 148 ms |
| sqlite-fts5 | 13 | 10 | 454 ms | 4.9 s |
| tantivy | 13 | 2,006 | 0.77 ms | 69 ms |

## Incremental update

| engine | documents | wall | docs/s | index after | growth |
| --- | --- | --- | --- | --- | --- |
| genba | 5,000 | 2m54s | 28 | 1.13 GB | -0.5% |
| seekstorm | 5,000 | 24.5 s | 204 | 377.2 MB | +8.9% |
| sqlite-fts5 | 5,000 | 22.2 s | 224 | 704.3 MB | +8.5% |
| tantivy | 5,000 | 4.5 s | 1,106 | 306.2 MB | +9.6% |

Growth is what rewriting documents the index already had cost in space.
An engine that never reclaims the old copies grows by roughly the size of the update.

## Per query

| query | engine | hits | median | p99 |
| --- | --- | --- | --- | --- |
| memory allocation | genba | 4,753 | 133 ms | 181 ms |
| memory allocation | seekstorm | 4,315 | 1.93 ms | 4.01 ms |
| memory allocation | sqlite-fts5 | 4,753 | 60 ms | 164 ms |
| memory allocation | tantivy | 4,753 | 0.41 ms | 1.22 ms |
| return value | genba | 32,997 | 545 ms | 793 ms |
| return value | seekstorm | 32,158 | 5.16 ms | 33 ms |
| return value | sqlite-fts5 | 32,997 | 271 ms | 475 ms |
| return value | tantivy | 32,997 | 1.27 ms | 32 ms |
| error handling | genba | 31,008 | 446 ms | 503 ms |
| error handling | seekstorm | 29,933 | 0.86 ms | 26 ms |
| error handling | sqlite-fts5 | 31,008 | 207 ms | 313 ms |
| error handling | tantivy | 31,008 | 0.97 ms | 2.22 ms |
| deadlock detection | genba | 804 | 71 ms | 130 ms |
| deadlock detection | seekstorm | 752 | 0.76 ms | 45 ms |
| deadlock detection | sqlite-fts5 | 804 | 8.54 ms | 39 ms |
| deadlock detection | tantivy | 804 | 0.17 ms | 2.14 ms |
| reference counting | genba | 4,317 | 96 ms | 114 ms |
| reference counting | seekstorm | 4,058 | 0.59 ms | 0.79 ms |
| reference counting | sqlite-fts5 | 4,317 | 30 ms | 59 ms |
| reference counting | tantivy | 4,317 | 0.24 ms | 1.09 ms |
| buffer overflow check | genba | 19,602 | 322 ms | 379 ms |
| buffer overflow check | seekstorm | 13,443 | 4.60 ms | 33 ms |
| buffer overflow check | sqlite-fts5 | 19,602 | 143 ms | 197 ms |
| buffer overflow check | tantivy | 19,602 | 1.21 ms | 26 ms |
| thread pool shutdown | genba | 3,763 | 163 ms | 259 ms |
| thread pool shutdown | seekstorm | 3,180 | 1.12 ms | 45 ms |
| thread pool shutdown | sqlite-fts5 | 3,763 | 39 ms | 90 ms |
| thread pool shutdown | tantivy | 3,763 | 0.34 ms | 0.45 ms |
| mmap_region | genba | 1,373 | 96 ms | 185 ms |
| mmap_region | seekstorm | 0 | 0.04 ms | 0.28 ms |
| mmap_region | sqlite-fts5 | 1,373 | 14 ms | 43 ms |
| mmap_region | tantivy | 0 | 0.07 ms | 0.10 ms |
| kasan | genba | 9 | 28 ms | 80 ms |
| kasan | seekstorm | 3 | 0.26 ms | 0.48 ms |
| kasan | sqlite-fts5 | 9 | 0.90 ms | 2.23 ms |
| kasan | tantivy | 9 | 0.06 ms | 0.10 ms |
| tsan_atomic | genba | 2,394 | 133 ms | 252 ms |
| tsan_atomic | seekstorm | 0 | 0.09 ms | 0.29 ms |
| tsan_atomic | sqlite-fts5 | 2,394 | 20 ms | 48 ms |
| tsan_atomic | tantivy | 0 | 0.05 ms | 0.08 ms |
| backwards compatibility guarantee | genba | 2,473 | 147 ms | 281 ms |
| backwards compatibility guarantee | seekstorm | 2,231 | 0.75 ms | 35 ms |
| backwards compatibility guarantee | sqlite-fts5 | 2,473 | 18 ms | 51 ms |
| backwards compatibility guarantee | tantivy | 2,473 | 0.27 ms | 0.54 ms |
| deprecated in favour of | genba | 1,615 | 118 ms | 189 ms |
| deprecated in favour of | seekstorm | 51,164 | 2.54 ms | 5.83 ms |
| deprecated in favour of | sqlite-fts5 | 53,302 | 454 ms | 636 ms |
| deprecated in favour of | tantivy | 53,301 | 2.09 ms | 33 ms |
| the | genba | 51,086 | 1.3 s | 1.5 s |
| the | seekstorm | 50,989 | 9.61 ms | 162 ms |
| the | sqlite-fts5 | 51,086 | 403 ms | 521 ms |
| the | tantivy | 51,086 | 0.81 ms | 1.63 ms |

Two engines that disagree about the hit count are not answering the same question, and their latencies are not comparable.

