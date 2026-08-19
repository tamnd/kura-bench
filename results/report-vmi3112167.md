# Results on vmi3112167

20 timed runs per query.

## Machine

vmi3112167, linux/amd64, AMD EPYC Processor (with IBPB), 6 cores, 11.68 GB of memory.
Load before the run was 8.31 and 6.26 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Corpus

82,791 documents, 464.0 MB of text.

## Indexing

| engine | version | wall | docs/s | MB/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| genba | v0.0.0-20260819135620-17146945489f | 22m26s | 61 | 0.3 | 1245.9 | 0.9x | 206.6 MB |
| seekstorm | 3.3.5 | 2m58s | 462 | 2.6 | 247.8 | 1.4x | 3.88 GB |
| sqlite-fts5 | v1.56.0 | 1m55s | 717 | 4.0 | 107.4 | 0.9x | 66.1 MB |
| tantivy | tantivy v0.26.1, index_format v7 | 19.2 s | 4,301 | 24.1 | 50.2 | 2.6x | 495.2 MB |

## Storage

| engine | index on disk | files | index over corpus | bytes written |
| --- | --- | --- | --- | --- |
| genba | 1.13 GB | 3 | 2.50x | 4.86 GB |
| seekstorm | 342.5 MB | 45 | 0.74x | 342.6 MB |
| sqlite-fts5 | 648.8 MB | 3 | 1.40x | 2.37 GB |
| tantivy | 278.6 MB | 22 | 0.60x | 550.7 MB |

Index over corpus below one means the engine does not keep the document text.
Bytes written is what the process asked the kernel to write, which on some platforms counts every handle and not only files.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| genba | 7.1 s | 1.24 | 35.4 MB |
| seekstorm | 907 ms | 1.21 | 152.1 MB |
| sqlite-fts5 | 53 ms | 0.05 | 12.4 MB |
| tantivy | 4 ms | 0.01 | 10.7 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Search, one query at a time

| engine | median | p90 | p99 | CPU ms per query | peak RSS |
| --- | --- | --- | --- | --- | --- |
| genba | 147 ms | 1.0 s | 2.5 s | 366.7 | 128.7 MB |
| seekstorm | 0.96 ms | 19 ms | 58 ms | 3.5 | 167.9 MB |
| sqlite-fts5 | 34 ms | 532 ms | 950 ms | 117.4 | 16.1 MB |
| tantivy | 0.26 ms | 1.29 ms | 16 ms | 0.7 | 30.4 MB |

## Search, several in flight

| engine | workers | queries/s | median | p99 |
| --- | --- | --- | --- | --- |
| genba | 13 | 2 | 2.7 s | 20.8 s |
| seekstorm | 13 | 485 | 18 ms | 144 ms |
| sqlite-fts5 | 13 | 6 | 771 ms | 8.9 s |
| tantivy | 13 | 3,708 | 0.68 ms | 14 ms |

## Incremental update

| engine | documents | wall | docs/s | index after | growth |
| --- | --- | --- | --- | --- | --- |
| genba | 5,000 | 4m05s | 20 | 1.13 GB | -0.2% |
| seekstorm | 5,000 | 25.4 s | 196 | 372.9 MB | +8.9% |
| sqlite-fts5 | 5,000 | 19.1 s | 261 | 704.3 MB | +8.5% |
| tantivy | 5,000 | 1.9 s | 2,663 | 306.5 MB | +10.0% |

Growth is what rewriting documents the index already had cost in space.
An engine that never reclaims the old copies grows by roughly the size of the update.

## Per query

| query | engine | hits | median | p99 |
| --- | --- | --- | --- | --- |
| memory allocation | genba | 4,753 | 207 ms | 534 ms |
| memory allocation | seekstorm | 4,315 | 1.60 ms | 2.93 ms |
| memory allocation | sqlite-fts5 | 4,753 | 28 ms | 64 ms |
| memory allocation | tantivy | 4,753 | 0.41 ms | 0.47 ms |
| return value | genba | 32,997 | 779 ms | 1.5 s |
| return value | seekstorm | 32,158 | 4.47 ms | 16 ms |
| return value | sqlite-fts5 | 32,997 | 212 ms | 594 ms |
| return value | tantivy | 32,997 | 1.16 ms | 1.55 ms |
| error handling | genba | 31,008 | 738 ms | 1.3 s |
| error handling | seekstorm | 29,933 | 0.86 ms | 4.20 ms |
| error handling | sqlite-fts5 | 31,008 | 144 ms | 438 ms |
| error handling | tantivy | 31,008 | 0.96 ms | 1.15 ms |
| deadlock detection | genba | 804 | 136 ms | 532 ms |
| deadlock detection | seekstorm | 752 | 1.07 ms | 13 ms |
| deadlock detection | sqlite-fts5 | 804 | 7.01 ms | 8.21 ms |
| deadlock detection | tantivy | 804 | 0.13 ms | 0.29 ms |
| reference counting | genba | 4,317 | 119 ms | 671 ms |
| reference counting | seekstorm | 4,058 | 0.64 ms | 1.01 ms |
| reference counting | sqlite-fts5 | 4,317 | 39 ms | 78 ms |
| reference counting | tantivy | 4,317 | 0.23 ms | 0.32 ms |
| buffer overflow check | genba | 19,602 | 393 ms | 780 ms |
| buffer overflow check | seekstorm | 13,443 | 6.19 ms | 58 ms |
| buffer overflow check | sqlite-fts5 | 19,602 | 108 ms | 306 ms |
| buffer overflow check | tantivy | 19,602 | 1.08 ms | 1.39 ms |
| thread pool shutdown | genba | 3,763 | 179 ms | 712 ms |
| thread pool shutdown | seekstorm | 3,180 | 1.41 ms | 5.56 ms |
| thread pool shutdown | sqlite-fts5 | 3,763 | 34 ms | 151 ms |
| thread pool shutdown | tantivy | 3,763 | 0.26 ms | 0.33 ms |
| mmap_region | genba | 1,373 | 87 ms | 605 ms |
| mmap_region | seekstorm | 0 | 0.12 ms | 1.92 ms |
| mmap_region | sqlite-fts5 | 1,373 | 18 ms | 57 ms |
| mmap_region | tantivy | 0 | 0.04 ms | 0.06 ms |
| kasan | genba | 9 | 33 ms | 159 ms |
| kasan | seekstorm | 3 | 0.32 ms | 0.36 ms |
| kasan | sqlite-fts5 | 9 | 0.83 ms | 1.05 ms |
| kasan | tantivy | 9 | 0.05 ms | 0.17 ms |
| tsan_atomic | genba | 2,394 | 122 ms | 299 ms |
| tsan_atomic | seekstorm | 0 | 0.07 ms | 0.21 ms |
| tsan_atomic | sqlite-fts5 | 2,394 | 15 ms | 59 ms |
| tsan_atomic | tantivy | 0 | 0.03 ms | 0.04 ms |
| backwards compatibility guarantee | genba | 2,473 | 147 ms | 314 ms |
| backwards compatibility guarantee | seekstorm | 2,231 | 0.96 ms | 21 ms |
| backwards compatibility guarantee | sqlite-fts5 | 2,473 | 14 ms | 29 ms |
| backwards compatibility guarantee | tantivy | 2,473 | 0.21 ms | 0.39 ms |
| deprecated in favour of | genba | 1,615 | 96 ms | 293 ms |
| deprecated in favour of | seekstorm | 51,164 | 6.78 ms | 30 ms |
| deprecated in favour of | sqlite-fts5 | 53,302 | 334 ms | 743 ms |
| deprecated in favour of | tantivy | 53,301 | 2.26 ms | 16 ms |
| the | genba | 51,086 | 1.4 s | 2.5 s |
| the | seekstorm | 50,989 | 0.45 ms | 16 ms |
| the | sqlite-fts5 | 51,086 | 318 ms | 950 ms |
| the | tantivy | 51,086 | 0.75 ms | 2.81 ms |

Two engines that disagree about the hit count are not answering the same question, and their latencies are not comparable.

