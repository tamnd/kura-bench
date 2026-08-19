# Graph results on vmi3391933

The Google web graph from the 2002 programming contest, 875,713 pages and 5,105,039 links.

The plan is 1000 neighbour lookups, 100 two hop lookups, 100 shortest paths, 10 full traversals, and pagerank over 20 iterations at damping 0.85.
The nodes are a fixed sample, so every engine is asked about the same ones in the same order, and a run with fewer of them is a subset of a run with more.

## Machine

vmi3391933, linux/x86_64, AMD EPYC Processor (with IBPB), 8 cores, 23.47 GB of memory.
Load before the run was 12.27 and 6.25 GB was free, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Graph

web-google, 875,713 nodes and 5,105,039 edges, directed.
The run asked about 1,000 nodes, the same ones in the same order for every engine.
The edge list is 38.9 MB as pairs of uint32, which is what the store sizes below are measured against.

## Correctness

| engine | version | neighbours | two-hop | shortest-path | bfs | pagerank |
| --- | --- | --- | --- | --- | --- | --- |
| csr | 0.1.0 | agrees | agrees | agrees | agrees | agrees |
| ladybug | 0.19.1 | agrees | agrees | agrees | 20.0% agree | cannot |
| petgraph | 0.8.3 | agrees | agrees | agrees | agrees | agrees |
| sqlite | v1.57.0 | ran out of time | ran out of time | ran out of time | ran out of time | ran out of time |

The answers were worked out separately, in Go, by walking the same edge list the plainest way there is.
Agreement between that and an engine is two independent implementations landing on the same numbers, which is the only reason the timings below are worth reading.

## Loading the graph

| engine | wall | edges/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- |
| csr | 6.1 s | 839,894 | 5.3 | 0.9x | 112.7 MB |
| ladybug | 13.8 s | 369,605 | 25.0 | 1.8x | 655.7 MB |
| petgraph | 6.7 s | 762,780 | 6.2 | 0.9x | 165.3 MB |
| sqlite | 1m53s | 45,106 | 68.3 | 0.6x | 574.8 MB |

This is the cost of getting a graph into the store in the first place, which on a large one is the largest number in this report.

## Storage

| engine | store on disk | files | bytes per edge | store over edge list |
| --- | --- | --- | --- | --- |
| csr | 29.5 MB | 1 | 6.1 | 0.76x |
| ladybug | 175.9 MB | 3 | 36.1 | 4.52x |
| petgraph | 38.9 MB | 1 | 8.0 | 1.00x |
| sqlite | 161.5 MB | 3 | 33.2 | 4.15x |

Below one means the store is keeping less than the eight bytes an edge arrived as, which is what a dense adjacency buys.
Above one is the cost of an index, a property store or a page layout, and it should be buying something back in the tables below.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| csr | 269 ms | 0.16 | 32.0 MB |
| ladybug | 417 ms | 0.24 | 73.7 MB |
| petgraph | 6.8 s | 6.11 | 164.8 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Operations

### neighbours

One hop out of one node, the cheapest thing a graph store does and the one it does most.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1,000 | 1.3 us | 2.2 us | 3.8 us | 83.4 us | 91,889 | 8 in flight |
| ladybug | 1,000 | 4.70 ms | 22.52 ms | 76.94 ms | 172.20 ms | 298 | 8 in flight |
| petgraph | 1,000 | 1.8 us | 2.5 us | 12.0 us | 466.0 us | 285,440 | 8 in flight |
| sqlite | ran out of time | | | | | | |

### two-hop

The distinct nodes within two hops, which is a friend of a friend, and the operation where the cost of a hub shows up.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 30.5 us | 228.9 us | 4.20 ms | 4.22 ms | 4,108 | 8 in flight |
| ladybug | 100 | 9.58 ms | 44.18 ms | 82.34 ms | 92.29 ms | 85 | 8 in flight |
| petgraph | 100 | 40.7 us | 317.9 us | 786.0 us | 28.59 ms | 2,339 | 8 in flight |
| sqlite | ran out of time | | | | | | |

### shortest-path

The hop count between two nodes, or nothing when they are not connected, which costs a full traversal of everything the start can reach.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 111.94 ms | 375.05 ms | 464.28 ms | 481.43 ms | 24 | 8 in flight |
| ladybug | 100 | 1.49 s | 3.23 s | 4.20 s | 4.57 s | 0 | 8 in flight |
| petgraph | 100 | 117.67 ms | 424.58 ms | 515.25 ms | 574.10 ms | 19 | 8 in flight |
| sqlite | ran out of time | | | | | | |

### bfs

The whole reachable set from one node, which touches everything and cannot be helped by any index.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 10 | 294.99 ms | 327.90 ms | 402.69 ms | 402.69 ms | 3 | one at a time |
| ladybug | 10 | 4.70 s | 9.08 s | 9.38 s | 9.38 s | 0 | one at a time |
| petgraph | 10 | 396.26 ms | 466.85 ms | 474.44 ms | 474.44 ms | 2 | one at a time |
| sqlite | ran out of time | | | | | | |

### pagerank

The whole graph, several times over, which is the analytics workload rather than the serving one.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1 | 3.11 s | 3.11 s | 3.11 s | 3.11 s | 0 | one at a time |
| ladybug | | | | | | | its PageRank lives in an extension that is downloaded at first use, and a benchmark that reaches the network mid run is measuring the network |
| petgraph | 1 | 11.39 s | 11.39 s | 11.39 s | 11.39 s | 0 | one at a time |
| sqlite | ran out of time | | | | | | |

The maximum matters more here than in the other suites.
Most nodes in a real graph have a handful of neighbours and a few have a hundred thousand, so the median says what the common case costs and the maximum says what a hub costs.
A cell that says below the clock is a lookup that finished inside one tick of the monotonic timer, which is a real result on a small graph and not a missing one.

## Notes

- ladybug: a property graph database reached through its C API, loaded with COPY from a CSV the way its own documentation loads a graph, and queried in Cypher rather than by walking the adjacency from the runner, and its variable length patterns take an upper bound of at most 30 hops, which this graph is deeper than, so the traversals were cut off there and the correctness table says so rather than the timings quietly being for a smaller job
- petgraph: petgraph is an in memory library with no on disk form, so the build phase writes the edges back out and the cold start is the graph being constructed again, so its open figure is a rebuild rather than a file being mapped
- sqlite: the query phase did not finish within 45m0s, which is why its rows are empty rather than the engine being absent from the run. The edges live in one table with a covering index on both columns, and the traversals walk it a level at a time from Go, which is how a relational store is used as a graph in practice

