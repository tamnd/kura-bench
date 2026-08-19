# Graph results on USERnoMacBook-Air.local

Graph ca-grqc from graphdata/ca-grqc, The Arxiv General Relativity collaboration network, 5,242 authors and 14,490 collaborations stored in both directions.

The plan is 1000 neighbour lookups, 100 two hop lookups, 100 shortest paths, 10 full traversals, and pagerank over 20 iterations at damping 0.85.
The nodes are a fixed sample, so every engine is asked about the same ones in the same order, and a run with fewer of them is a subset of a run with more.

## Machine

USERnoMacBook-Air.local, macos/aarch64, Apple M4, 10 cores, 24.00 GB of memory.
Load before the run was 18.76, so the machine was doing other work and these numbers are a floor rather than a measurement.

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
| sqlite | v1.56.0 | agrees | agrees | agrees | agrees | agrees |

The answers were worked out separately, in Go, by walking the same edge list the plainest way there is.
Agreement between that and an engine is two independent implementations landing on the same numbers, which is the only reason the timings below are worth reading.

## Loading the graph

| engine | wall | edges/s | CPU s | parallelism | peak RSS |
| --- | --- | --- | --- | --- | --- |
| csr | 2 ms | 16,252,748 | 0.0 | 0.7x | 2.9 MB |
| ladybug | 124 ms | 233,883 | 0.1 | 0.7x | 164.1 MB |
| petgraph | 1 ms | 20,355,297 | 0.0 | 1.0x | 2.9 MB |
| sqlite | 62 ms | 469,995 | 0.1 | 1.0x | 20.3 MB |

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
| ladybug | 32 ms | 0.03 | 400 KB |
| petgraph | 1 ms | 0.00 | 3.0 MB |
| sqlite | 1 ms | 0.00 | 2.5 MB |

This is a separate process from the one that built the index, so it is a real restart and not a reopen of a warm handle.

## Operations

### neighbours

One hop out of one node, the cheapest thing a graph store does and the one it does most.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1,000 | below the clock | 42 ns | 42 ns | 125 ns | 5,278,186 | 10 in flight |
| ladybug | 1,000 | 142.8 us | 339.6 us | 1.11 ms | 3.92 ms | 19,259 | 10 in flight |
| petgraph | 1,000 | 42 ns | 83 ns | 125 ns | 208 ns | 3,360,395 | 10 in flight |
| sqlite | 1,000 | 6.7 us | 8.9 us | 11.7 us | 23.0 us | 135,669 | 10 in flight |

### two-hop

The distinct nodes within two hops, which is a friend of a friend, and the operation where the cost of a hub shows up.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 125 ns | 542 ns | 1.5 us | 1.9 us | 1,166,738 | 10 in flight |
| ladybug | 100 | 556.3 us | 657.9 us | 855.5 us | 1.76 ms | 3,244 | 10 in flight |
| petgraph | 100 | 250 ns | 1.1 us | 3.7 us | 4.6 us | 646,028 | 10 in flight |
| sqlite | 100 | 26.4 us | 92.3 us | 515.2 us | 520.3 us | 22,529 | 10 in flight |

### shortest-path

The hop count between two nodes, or nothing when they are not connected, which costs a full traversal of everything the start can reach.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 100 | 14.7 us | 64.1 us | 68.1 us | 68.8 us | 184,275 | 10 in flight |
| ladybug | 100 | 1.53 ms | 4.34 ms | 5.43 ms | 5.58 ms | 471 | 10 in flight |
| petgraph | 100 | 28.9 us | 123.6 us | 128.8 us | 131.7 us | 115,168 | 10 in flight |
| sqlite | 100 | 4.75 ms | 31.92 ms | 34.58 ms | 35.40 ms | 100 | 10 in flight |

### bfs

The whole reachable set from one node, which touches everything and cannot be helped by any index.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 10 | 66.0 us | 68.4 us | 69.3 us | 69.3 us | 15,151 | one at a time |
| ladybug | 10 | 5.98 ms | 6.29 ms | 6.43 ms | 6.43 ms | 167 | one at a time |
| petgraph | 10 | 122.6 us | 125.7 us | 126.4 us | 126.4 us | 8,157 | one at a time |
| sqlite | 10 | 43.09 ms | 51.52 ms | 60.53 ms | 60.53 ms | 23 | one at a time |

### pagerank

The whole graph, several times over, which is the analytics workload rather than the serving one.

| engine | runs | median | p90 | p99 | max | per second | measured |
| --- | --- | --- | --- | --- | --- | --- | --- |
| csr | 1 | 620.3 us | 620.3 us | 620.3 us | 620.3 us | 1,612 | one at a time |
| ladybug | | | | | | | its PageRank lives in an extension that is downloaded at first use, and a benchmark that reaches the network mid run is measuring the network |
| petgraph | 1 | 1.64 ms | 1.64 ms | 1.64 ms | 1.64 ms | 610 | one at a time |
| sqlite | 1 | 185.61 ms | 185.61 ms | 185.61 ms | 185.61 ms | 5 | one at a time |

The maximum matters more here than in the other suites.
Most nodes in a real graph have a handful of neighbours and a few have a hundred thousand, so the median says what the common case costs and the maximum says what a hub costs.
A cell that says below the clock is a lookup that finished inside one tick of the monotonic timer, which is a real result on a small graph and not a missing one.

## Notes

- ladybug: a property graph database reached through its C API, loaded with COPY from a CSV the way its own documentation loads a graph, and queried in Cypher rather than by walking the adjacency from the runner
- petgraph: petgraph is an in memory library with no on disk form, so the build phase writes the edges back out and the cold start is the graph being constructed again, so its open figure is a rebuild rather than a file being mapped
- sqlite: the edges live in one table with a covering index on both columns, and the traversals walk it a level at a time from Go, which is how a relational store is used as a graph in practice

