# Graph results on USERnoMacBook-Air.local

The Arxiv General Relativity collaboration network, 5,242 authors and 14,490 collaborations stored in both directions.

The plan is 1000 neighbour lookups, 100 two hop lookups, 100 shortest paths, 10 full traversals, and pagerank over 20 iterations at damping 0.85.
The nodes are a fixed sample, so every engine is asked about the same ones in the same order, and a run with fewer of them is a subset of a run with more.

## Machine

USERnoMacBook-Air.local, macos/aarch64, Apple M4, 10 cores, 24.00 GB of memory.
Load before the run was 16.32, so the machine was doing other work and these numbers are a floor rather than a measurement.

## Graph

ca-grqc, 5,242 nodes and 28,980 edges, undirected, stored with both directions of every edge.
The run asked about 1,000 nodes, the same ones in the same order for every engine.
The edge list is 226 KB as pairs of uint32, which is what the store sizes below are measured against.

## Correctness

| engine | version | neighbours | two-hop | shortest-path | bfs | pagerank |
| --- | --- | --- | --- | --- | --- | --- |
| csr | 0.1.0 | agrees | agrees | agrees | agrees | agrees |
| ladybug | 0.19.1 | agrees | agrees | agrees | agrees | cannot |
| petgraph | 0.8.3 | agrees | agrees | agrees | agrees | agrees |
| sqlite | v1.57.0 | agrees | agrees | agrees | agrees | agrees |

The answers were worked out separately, in Go, by walking the same edge list the plainest way there is.
Agreement between that and an engine is two independent implementations landing on the same numbers, which is the only reason the timings below are worth reading.

## Loading the graph

| engine | wall | edges/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- |
| csr | 2 ms | 18,421,930 | 0.0 | 0.8x | 2.8 MB |
| ladybug | 146 ms | 198,341 | 0.1 | 0.7x | 154.8 MB |
| petgraph | 1 ms | 26,131,650 | 0.0 | 1.0x | 3.2 MB |
| sqlite | 64 ms | 450,433 | 0.1 | 0.9x | 19.9 MB |

This is the cost of getting a graph into the store in the first place, which on a large one is the largest number in this report.

## Storage

| engine | store on disk | files | bytes per edge | store over edge list |
| --- | --- | --- | --- | --- |
| csr | 175 KB | 1 | 6.2 | 0.77x |
| ladybug | 2.7 MB | 3 | 96.9 | 12.12x |
| petgraph | 226 KB | 1 | 8.0 | 1.00x |
| sqlite | 824 KB | 3 | 29.1 | 3.64x |

Below one means the store is keeping less than the eight bytes an edge arrived as, which is what a dense adjacency buys.
Above one is the cost of an index, a property store or a page layout, and it should be buying something back in the tables below.

## Cold start

| engine | open and first query | CPU s | resident after open |
| --- | --- | --- | --- |
| csr | 0 ms | 0.00 | 2.4 MB |
| ladybug | 30 ms | 0.03 | 400 KB |
| petgraph | 1 ms | 0.00 | 3.1 MB |
| sqlite | 1 ms | 0.00 | 2.5 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Operations

### neighbours

One hop out of one node, the cheapest thing a graph store does and the one it does most.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1,000 | below the clock | 42 ns | 42 ns | 125 ns | 3,724,977 | 10 in flight |
| ladybug | 1,000 | 185.7 us | 254.0 us | 462.8 us | 638.0 us | 16,958 | 10 in flight |
| petgraph | 1,000 | 42 ns | 83 ns | 125 ns | 167 ns | 4,244,788 | 10 in flight |
| sqlite | 1,000 | 5.3 us | 6.8 us | 10.8 us | 27.2 us | 185,238 | 10 in flight |

### two-hop

The distinct nodes within two hops, which is a friend of a friend, and the operation where the cost of a hub shows up.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 166 ns | 666 ns | 1.7 us | 2.1 us | 28,393 | 10 in flight |
| ladybug | 100 | 671.9 us | 786.8 us | 1.00 ms | 1.55 ms | 3,206 | 10 in flight |
| petgraph | 100 | 167 ns | 792 ns | 3.0 us | 3.7 us | 859,291 | 10 in flight |
| sqlite | 100 | 20.5 us | 91.4 us | 419.8 us | 428.0 us | 34,832 | 10 in flight |

### shortest-path

The hop count between two nodes, or nothing when they are not connected, which costs a full traversal of everything the start can reach.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 14.9 us | 66.8 us | 70.1 us | 77.9 us | 169,372 | 10 in flight |
| ladybug | 100 | 1.36 ms | 3.74 ms | 4.32 ms | 4.43 ms | 573 | 10 in flight |
| petgraph | 100 | 25.1 us | 109.0 us | 114.3 us | 119.0 us | 144,005 | 10 in flight |
| sqlite | 100 | 3.43 ms | 23.70 ms | 26.92 ms | 27.54 ms | 191 | 10 in flight |

### bfs

The whole reachable set from one node, which touches everything and cannot be helped by any index.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 10 | 64.5 us | 65.9 us | 68.5 us | 68.5 us | 15,513 | one at a time |
| ladybug | 10 | 4.89 ms | 5.02 ms | 5.16 ms | 5.16 ms | 204 | one at a time |
| petgraph | 10 | 106.4 us | 119.1 us | 121.5 us | 121.5 us | 9,400 | one at a time |
| sqlite | 10 | 24.13 ms | 24.43 ms | 24.75 ms | 24.75 ms | 41 | one at a time |

### pagerank

The whole graph, several times over, which is the analytics workload rather than the serving one.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1 | 608.5 us | 608.5 us | 608.5 us | 608.5 us | 1,643 | one at a time |
| ladybug | | | | | | | its PageRank lives in an extension that is downloaded at first use, and a benchmark that reaches the network mid run is measuring the network |
| petgraph | 1 | 1.38 ms | 1.38 ms | 1.38 ms | 1.38 ms | 725 | one at a time |
| sqlite | 1 | 106.06 ms | 106.06 ms | 106.06 ms | 106.06 ms | 9 | one at a time |

The maximum matters more here than in the other suites.
Most nodes in a real graph have a handful of neighbours and a few have a hundred thousand, so the median says what the common case costs and the maximum says what a hub costs.
A cell that says below the clock is a lookup that finished inside one tick of the monotonic timer, which is a real result on a small graph and not a missing one.

## Notes

- ladybug: a property graph database reached through its C API, loaded with COPY from a CSV the way its own documentation loads a graph, and queried in Cypher rather than by walking the adjacency from the runner
- petgraph: petgraph is an in memory library with no on disk form, so the build phase writes the edges back out and the cold start is the graph being constructed again, so its open figure is a rebuild rather than a file being mapped
- sqlite: the edges live in one table with a covering index on both columns, and the traversals walk it a level at a time from Go, which is how a relational store is used as a graph in practice

