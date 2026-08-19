# Results on vmi3112167

20 timed runs per query.

## Machine

vmi3112167, linux/amd64, AMD EPYC Processor (with IBPB), 6 cores, 11.68 GB of memory.
Load before the run was 9.29 and 6.26 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Corpus

82,791 documents, 464.0 MB of text.

## Indexing

| engine | version | wall | docs/s | MB/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- |
| bleve | v2.6.0 | 2m19s | 591 | 3.3 | 188.1 | 1.3x | 502.3 MB |
| genba | v0.0.0-20260819135620-17146945489f | 22m26s | 61 | 0.3 | 1245.9 | 0.9x | 206.6 MB |
| seekstorm | 3.3.5 | 2m58s | 462 | 2.6 | 247.8 | 1.4x | 3.88 GB |
| sqlite-fts5 | v1.57.0 | 2m27s | 559 | 3.1 | 116.9 | 0.8x | 62.7 MB |
| tantivy | tantivy v0.26.1, index_format v7 | 19.2 s | 4,301 | 24.1 | 50.2 | 2.6x | 495.2 MB |

## Storage

| engine | index on disk | files | index over corpus | bytes written |
| --- | --- | --- | --- | --- |
| bleve | 295.9 MB | 17 | 0.64x | 846.3 MB |
| genba | 1.13 GB | 3 | 2.50x | 4.86 GB |
| seekstorm | 342.5 MB | 45 | 0.74x | 342.6 MB |
| sqlite-fts5 | 648.8 MB | 3 | 1.40x | 2.36 GB |
| tantivy | 278.6 MB | 22 | 0.60x | 550.7 MB |

Index over corpus below one means the engine does not keep the document text.
Bytes written is what the process asked the kernel to write, which on some platforms counts every handle and not only files.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| bleve | 26 ms | 0.05 | 33.9 MB |
| genba | 7.1 s | 1.24 | 35.4 MB |
| seekstorm | 907 ms | 1.21 | 152.1 MB |
| sqlite-fts5 | 106 ms | 0.11 | 13.6 MB |
| tantivy | 4 ms | 0.01 | 10.7 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Search, one query at a time

| engine | median | p90 | p99 | CPU ms per query | peak RSS |
| --- | --- | --- | --- | --- | --- |
| bleve | 4.20 ms | 52 ms | 221 ms | 21.1 | 85.5 MB |
| genba | 147 ms | 1.0 s | 2.5 s | 366.7 | 128.7 MB |
| seekstorm | 0.96 ms | 19 ms | 58 ms | 3.5 | 167.9 MB |
| sqlite-fts5 | 37 ms | 586 ms | 792 ms | 123.8 | 16.4 MB |
| tantivy | 0.26 ms | 1.29 ms | 16 ms | 0.7 | 30.4 MB |

## Search, several in flight

| engine | workers | queries/s | median | p99 |
| --- | --- | --- | --- | --- |
| bleve | 13 | 70 | 88 ms | 1.0 s |
| genba | 13 | 2 | 2.7 s | 20.8 s |
| seekstorm | 13 | 485 | 18 ms | 144 ms |
| sqlite-fts5 | 13 | 8 | 630 ms | 6.4 s |
| tantivy | 13 | 3,708 | 0.68 ms | 14 ms |

## Incremental update

| engine | documents | wall | docs/s | index after | growth |
| --- | --- | --- | --- | --- | --- |
| bleve | 5,000 | 10.7 s | 467 | 322.8 MB | +9.1% |
| genba | 5,000 | 4m05s | 20 | 1.13 GB | -0.2% |
| seekstorm | 5,000 | 25.4 s | 196 | 372.9 MB | +8.9% |
| sqlite-fts5 | 5,000 | 19.9 s | 251 | 704.3 MB | +8.5% |
| tantivy | 5,000 | 1.9 s | 2,663 | 306.5 MB | +10.0% |

Growth is what rewriting documents the index already had cost in space.
An engine that never reclaims the old copies grows by roughly the size of the update.

## Per query

| query | engine | hits | median | p99 |
| --- | --- | --- | --- | --- |
| memory allocation | bleve | 4,753 | 3.48 ms | 13 ms |
| memory allocation | genba | 4,753 | 207 ms | 534 ms |
| memory allocation | seekstorm | 4,315 | 1.60 ms | 2.93 ms |
| memory allocation | sqlite-fts5 | 4,753 | 38 ms | 336 ms |
| memory allocation | tantivy | 4,753 | 0.41 ms | 0.47 ms |
| return value | bleve | 33,001 | 24 ms | 68 ms |
| return value | genba | 32,997 | 779 ms | 1.5 s |
| return value | seekstorm | 32,158 | 4.47 ms | 16 ms |
| return value | sqlite-fts5 | 32,997 | 201 ms | 766 ms |
| return value | tantivy | 32,997 | 1.16 ms | 1.55 ms |
| error handling | bleve | 31,005 | 28 ms | 123 ms |
| error handling | genba | 31,008 | 738 ms | 1.3 s |
| error handling | seekstorm | 29,933 | 0.86 ms | 4.20 ms |
| error handling | sqlite-fts5 | 31,008 | 186 ms | 365 ms |
| error handling | tantivy | 31,008 | 0.96 ms | 1.15 ms |
| deadlock detection | bleve | 804 | 1.21 ms | 8.19 ms |
| deadlock detection | genba | 804 | 136 ms | 532 ms |
| deadlock detection | seekstorm | 752 | 1.07 ms | 13 ms |
| deadlock detection | sqlite-fts5 | 804 | 7.03 ms | 8.52 ms |
| deadlock detection | tantivy | 804 | 0.13 ms | 0.29 ms |
| reference counting | bleve | 4,317 | 5.42 ms | 17 ms |
| reference counting | genba | 4,317 | 119 ms | 671 ms |
| reference counting | seekstorm | 4,058 | 0.64 ms | 1.01 ms |
| reference counting | sqlite-fts5 | 4,317 | 21 ms | 38 ms |
| reference counting | tantivy | 4,317 | 0.23 ms | 0.32 ms |
| buffer overflow check | bleve | 19,605 | 13 ms | 57 ms |
| buffer overflow check | genba | 19,602 | 393 ms | 780 ms |
| buffer overflow check | seekstorm | 13,443 | 6.19 ms | 58 ms |
| buffer overflow check | sqlite-fts5 | 19,602 | 115 ms | 302 ms |
| buffer overflow check | tantivy | 19,602 | 1.08 ms | 1.39 ms |
| thread pool shutdown | bleve | 3,765 | 4.20 ms | 16 ms |
| thread pool shutdown | genba | 3,763 | 179 ms | 712 ms |
| thread pool shutdown | seekstorm | 3,180 | 1.41 ms | 5.56 ms |
| thread pool shutdown | sqlite-fts5 | 3,763 | 37 ms | 275 ms |
| thread pool shutdown | tantivy | 3,763 | 0.26 ms | 0.33 ms |
| mmap_region | bleve | 1,380 | 1.40 ms | 36 ms |
| mmap_region | genba | 1,373 | 87 ms | 605 ms |
| mmap_region | seekstorm | 0 | 0.12 ms | 1.92 ms |
| mmap_region | sqlite-fts5 | 1,373 | 13 ms | 83 ms |
| mmap_region | tantivy | 0 | 0.04 ms | 0.06 ms |
| kasan | bleve | 9 | 0.62 ms | 6.67 ms |
| kasan | genba | 9 | 33 ms | 159 ms |
| kasan | seekstorm | 3 | 0.32 ms | 0.36 ms |
| kasan | sqlite-fts5 | 9 | 0.81 ms | 5.11 ms |
| kasan | tantivy | 9 | 0.05 ms | 0.17 ms |
| tsan_atomic | bleve | 2,399 | 1.32 ms | 7.09 ms |
| tsan_atomic | genba | 2,394 | 122 ms | 299 ms |
| tsan_atomic | seekstorm | 0 | 0.07 ms | 0.21 ms |
| tsan_atomic | sqlite-fts5 | 2,394 | 13 ms | 25 ms |
| tsan_atomic | tantivy | 0 | 0.03 ms | 0.04 ms |
| backwards compatibility guarantee | bleve | 2,470 | 1.43 ms | 6.55 ms |
| backwards compatibility guarantee | genba | 2,473 | 147 ms | 314 ms |
| backwards compatibility guarantee | seekstorm | 2,231 | 0.96 ms | 21 ms |
| backwards compatibility guarantee | sqlite-fts5 | 2,473 | 20 ms | 33 ms |
| backwards compatibility guarantee | tantivy | 2,473 | 0.21 ms | 0.39 ms |
| deprecated in favour of | bleve | 53,301 | 29 ms | 221 ms |
| deprecated in favour of | genba | 1,615 | 96 ms | 293 ms |
| deprecated in favour of | seekstorm | 51,164 | 6.78 ms | 30 ms |
| deprecated in favour of | sqlite-fts5 | 53,302 | 327 ms | 792 ms |
| deprecated in favour of | tantivy | 53,301 | 2.26 ms | 16 ms |
| the | bleve | 51,086 | 18 ms | 37 ms |
| the | genba | 51,086 | 1.4 s | 2.5 s |
| the | seekstorm | 50,989 | 0.45 ms | 16 ms |
| the | sqlite-fts5 | 51,086 | 285 ms | 733 ms |
| the | tantivy | 51,086 | 0.75 ms | 2.81 ms |

Two engines that disagree about the hit count are not answering the same question, and their latencies are not comparable.

## Notes

- bleve: measured with its segment library held at zapx v17.1.9, which is newer than this release of bleve asks for, because the version it asks for cannot read back the postings of a term that appears in more than a thousand documents

