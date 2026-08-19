//! The baseline every graph store here is measured against.
//!
//! It keeps the graph as compressed sparse row: every node's neighbours laid
//! out one node after another, with an offset per node. That is the layout a
//! graph database is trying to beat, and it is worth having in the table
//! because it is what somebody gets for a hundred lines of code and no
//! dependency. A store that does not beat it on some operation has to be
//! earning its keep somewhere else.
//!
//! It is also the check on the rest of the suite. The answers were worked out
//! by a compressed sparse row implementation in Go, and this is a second one in
//! another language. If the two disagree, one of them is wrong and every other
//! correctness figure in the report is suspect.

use std::cell::RefCell;
use std::fs::File;
use std::io::{BufReader, BufWriter, Read, Write};
use std::path::{Path, PathBuf};

use benchrs::graph::config::Config;
use benchrs::graph::ops::Engine;
use benchrs::graph::{config, data, ops, result};
use benchrs::{machine, result as textresult, usage};

/// The four bytes at the front of the built store, so that a stale file from
/// another engine is refused rather than read as a graph.
const MAGIC: &[u8; 4] = b"KGRF";

fn main() {
    if let Err(err) = run() {
        eprintln!("csr-graphrunner: {err}");
        std::process::exit(1);
    }
}

fn run() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = config::from_env()?;

    let mut res = result::GraphResult {
        engine: "csr".to_string(),
        // There is no library here, so the version is this runner's. An array
        // of offsets and an array of targets does not have releases.
        version: env!("CARGO_PKG_VERSION").to_string(),
        language: "rust".to_string(),
        machine: machine::describe(),
        ..Default::default()
    };

    match cfg.phase.as_str() {
        "build" => build_phase(&cfg, &mut res)?,
        "query" => query_phase(&cfg, &mut res)?,
        "all" => {
            build_phase(&cfg, &mut res)?;
            res.notes = "the open phase ran in the same process as the build, so it is warmer than a real cold start".to_string();
            query_phase(&cfg, &mut res)?;
        }
        other => return Err(format!("unknown phase {other}").into()),
    }

    println!("{}", serde_json::to_string(&res)?);
    Ok(())
}

/// build_phase reads the edge list and writes the adjacency out.
///
/// The identifiers are mapped to dense indexes, which is what every store here
/// does internally, and the mapping is part of the build because it is part of
/// what a store has to do before it can answer anything.
fn build_phase(
    cfg: &Config,
    res: &mut result::GraphResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let start = usage::take();

    let (header, edges) = data::edges(&cfg.edges)?;
    eprintln!("read {} edges over {} nodes", header.edges, header.nodes);
    let csr = Csr::build(&edges, header.flags);
    csr.write(&store_path(&cfg.work))?;

    res.build.usage = usage::measure(&start);
    let (bytes, files) = textresult::dir_size(&cfg.work);
    res.build.bytes = bytes;
    res.build.files = files;

    res.dataset = result::GraphStats {
        name: cfg.dataset.clone(),
        nodes: csr.nodes(),
        edges: csr.edges(),
        undirected: header.undirected(),
        seeds: 0,
    };
    Ok(())
}

/// query_phase opens the store in a process that has not read it before, then
/// runs the operations.
fn query_phase(
    cfg: &Config,
    res: &mut result::GraphResult,
) -> Result<(), Box<dyn std::error::Error>> {
    let open = usage::take();
    let csr = Csr::open(&store_path(&cfg.work))?;

    let seeds = data::seeds(&cfg.seeds)?;
    if seeds.is_empty() {
        return Err("the seed file is empty".into());
    }
    // One lookup, so that the open figure includes whatever the first one
    // costs. On an array that is the page cache filling, and it is the largest
    // part of a restart.
    let _ = csr.neighbours(seeds[0]);
    csr.warm();

    res.open.usage = usage::measure(&open);
    res.open.resident_bytes = res.open.usage.rss_bytes;

    let answers = data::answers(&cfg.answers)?;
    if answers.nodes != csr.nodes() || answers.edges != csr.edges() {
        return Err(format!(
            "the answers describe a graph of {} nodes and {} edges, this store holds {} and {}",
            answers.nodes,
            answers.edges,
            csr.nodes(),
            csr.edges()
        )
        .into());
    }

    let start = usage::take();
    res.query.ops = ops::run(&csr, cfg, &seeds, &answers);
    res.query.usage = usage::measure(&start);
    for op in &res.query.ops {
        eprintln!("{} scored {:.4} over {} runs", op.op, op.correct, op.runs);
    }

    res.dataset = result::GraphStats {
        name: cfg.dataset.clone(),
        nodes: csr.nodes(),
        edges: csr.edges(),
        undirected: csr.undirected,
        seeds: seeds.len(),
    };
    Ok(())
}

/// Csr is the whole engine: the identifiers, the offsets and the targets.
struct Csr {
    /// The distinct node identifiers, ascending. A node's position here is its
    /// dense index everywhere else, and the ascending order is what makes the
    /// lookup from identifier to index a binary search rather than a map.
    ids: Vec<u32>,

    /// One more entry than there are nodes. The neighbours of node i are
    /// `target[offset[i]..offset[i + 1]]`.
    offset: Vec<u64>,

    /// The dense index of the far end of every edge.
    target: Vec<u32>,

    /// Whether the publisher stored both directions of every edge.
    undirected: bool,
}

/// The per thread working set a traversal needs.
///
/// It is a thread local rather than an allocation per call because a store that
/// allocated five million booleans every time somebody asked for a two hop
/// neighbourhood would be measuring the allocator. It is also not a field on
/// [`Csr`], because the operations take `&self` and run on several threads at
/// once during the throughput pass.
#[derive(Default)]
struct Scratch {
    /// A visited set cleared by bumping a counter rather than by walking it,
    /// which matters when a thousand traversals each touch a handful of nodes
    /// in a graph with five million.
    seen: Vec<u32>,
    now: u32,

    frontier: Vec<u32>,
    next: Vec<u32>,
}

impl Scratch {
    fn reset(&mut self, nodes: usize) {
        if self.seen.len() != nodes {
            self.seen = vec![0; nodes];
            self.now = 0;
        }
        self.now += 1;
        if self.now == 0 {
            // Wrapped, which takes four billion traversals and is still worth
            // handling because the alternative is a wrong answer rather than a
            // slow one.
            self.seen.iter_mut().for_each(|v| *v = 0);
            self.now = 1;
        }
        self.frontier.clear();
        self.next.clear();
    }

    fn mark(&mut self, i: u32) -> bool {
        let slot = &mut self.seen[i as usize];
        if *slot == self.now {
            return false;
        }
        *slot = self.now;
        true
    }
}

thread_local! {
    static SCRATCH: RefCell<Scratch> = RefCell::new(Scratch::default());
}

impl Csr {
    /// Turns an edge list into adjacency, counting first and filling second.
    fn build(edges: &[u32], flags: u32) -> Csr {
        let mut ids: Vec<u32> = edges.to_vec();
        ids.sort_unstable();
        ids.dedup();

        let mut offset = vec![0u64; ids.len() + 1];
        for pair in edges.chunks_exact(2) {
            offset[index_of(&ids, pair[0]) as usize + 1] += 1;
        }
        for i in 1..offset.len() {
            offset[i] += offset[i - 1];
        }

        // The cursor starts as a copy of the offsets so the offsets themselves
        // survive, which is cheaper than rebuilding them afterwards.
        let mut cursor = offset[..ids.len()].to_vec();
        let mut target = vec![0u32; edges.len() / 2];
        for pair in edges.chunks_exact(2) {
            let from = index_of(&ids, pair[0]) as usize;
            target[cursor[from] as usize] = index_of(&ids, pair[1]);
            cursor[from] += 1;
        }

        Csr {
            ids,
            offset,
            target,
            undirected: flags & data::UNDIRECTED != 0,
        }
    }

    fn nodes(&self) -> usize {
        self.ids.len()
    }

    fn edges(&self) -> usize {
        self.target.len()
    }

    /// The dense index of an identifier, or none.
    fn index(&self, id: u32) -> Option<u32> {
        self.ids.binary_search(&id).ok().map(|i| i as u32)
    }

    fn row(&self, i: u32) -> &[u32] {
        let lo = self.offset[i as usize] as usize;
        let hi = self.offset[i as usize + 1] as usize;
        &self.target[lo..hi]
    }

    /// Sizes this thread's working set before anything is timed.
    ///
    /// The first traversal on a thread has to allocate one counter per node,
    /// and on LiveJournal that is twenty megabytes. Paying it here puts it in
    /// the cold start, which is where it belongs, rather than in the maximum of
    /// whichever operation happened to run first.
    fn warm(&self) {
        SCRATCH.with(|s| s.borrow_mut().reset(self.nodes()));
    }

    fn write(&self, path: &Path) -> std::io::Result<()> {
        let mut f = BufWriter::with_capacity(1 << 20, File::create(path)?);
        f.write_all(MAGIC)?;
        f.write_all(&(self.ids.len() as u64).to_le_bytes())?;
        f.write_all(&(self.target.len() as u64).to_le_bytes())?;
        f.write_all(&(u32::from(self.undirected)).to_le_bytes())?;
        for v in &self.ids {
            f.write_all(&v.to_le_bytes())?;
        }
        for v in &self.offset {
            f.write_all(&v.to_le_bytes())?;
        }
        for v in &self.target {
            f.write_all(&v.to_le_bytes())?;
        }
        f.flush()
    }

    fn open(path: &Path) -> Result<Csr, Box<dyn std::error::Error>> {
        let mut f = BufReader::with_capacity(1 << 20, File::open(path)?);

        let mut magic = [0u8; 4];
        f.read_exact(&mut magic)?;
        if &magic != MAGIC {
            return Err(format!("{} was not written by this runner", path.display()).into());
        }
        let mut head = [0u8; 20];
        f.read_exact(&mut head)?;
        let nodes = u64::from_le_bytes(head[0..8].try_into().unwrap()) as usize;
        let edges = u64::from_le_bytes(head[8..16].try_into().unwrap()) as usize;
        let undirected = u32::from_le_bytes(head[16..20].try_into().unwrap()) != 0;

        let ids = read_u32(&mut f, nodes)?;
        let offset = read_u64(&mut f, nodes + 1)?;
        let target = read_u32(&mut f, edges)?;
        Ok(Csr {
            ids,
            offset,
            target,
            undirected,
        })
    }
}

/// The identifier of a dense index, which is what every answer is written in.
fn index_of(ids: &[u32], id: u32) -> u32 {
    ids.binary_search(&id).expect("edge endpoint is a node") as u32
}

impl Engine for Csr {
    fn neighbours(&self, node: u32) -> i64 {
        match self.index(node) {
            Some(i) => self.row(i).len() as i64,
            None => -1,
        }
    }

    fn two_hop(&self, node: u32) -> i64 {
        let Some(from) = self.index(node) else {
            return -1;
        };
        SCRATCH.with(|cell| {
            let mut s = cell.borrow_mut();
            s.reset(self.nodes());
            s.mark(from);
            let mut n = 0i64;
            for one in self.row(from) {
                if s.mark(*one) {
                    n += 1;
                }
            }
            // The frontier is re-read from the adjacency rather than collected,
            // because the marks already say which of them were new and the row
            // is still warm.
            for one in self.row(from) {
                for two in self.row(*one) {
                    if s.mark(*two) {
                        n += 1;
                    }
                }
            }
            n
        })
    }

    fn shortest_path(&self, from: u32, to: u32) -> i64 {
        let (Some(from), Some(to)) = (self.index(from), self.index(to)) else {
            return -1;
        };
        if from == to {
            return 0;
        }
        SCRATCH.with(|cell| {
            let mut s = cell.borrow_mut();
            s.reset(self.nodes());
            s.mark(from);

            // The two frontiers are moved out of the scratch and back again so
            // that the visited set can be written while they are being read.
            // They keep whatever capacity the last traversal gave them, which
            // is the point of having them in the scratch at all.
            let (mut cur, mut next) =
                (std::mem::take(&mut s.frontier), std::mem::take(&mut s.next));
            cur.push(from);

            let mut answer = -1i64;
            let mut depth = 1i64;
            'search: while !cur.is_empty() {
                next.clear();
                for n in &cur {
                    for m in self.row(*n) {
                        if *m == to {
                            answer = depth;
                            break 'search;
                        }
                        if s.mark(*m) {
                            next.push(*m);
                        }
                    }
                }
                std::mem::swap(&mut cur, &mut next);
                depth += 1;
            }

            s.frontier = cur;
            s.next = next;
            answer
        })
    }

    fn bfs(&self, node: u32) -> (i64, i64) {
        let Some(from) = self.index(node) else {
            return (-1, -1);
        };
        SCRATCH.with(|cell| {
            let mut s = cell.borrow_mut();
            s.reset(self.nodes());
            s.mark(from);

            let (mut cur, mut next) =
                (std::mem::take(&mut s.frontier), std::mem::take(&mut s.next));
            cur.push(from);

            let mut reached = 1i64;
            let mut depth = 0i64;
            while !cur.is_empty() {
                next.clear();
                for n in &cur {
                    for m in self.row(*n) {
                        if s.mark(*m) {
                            reached += 1;
                            next.push(*m);
                        }
                    }
                }
                if !next.is_empty() {
                    depth += 1;
                }
                std::mem::swap(&mut cur, &mut next);
            }

            s.frontier = cur;
            s.next = next;
            (reached, depth)
        })
    }

    /// PageRank, with the mass on nodes that have no outgoing edges spread over
    /// every node rather than dropped, which is what keeps the total at one.
    ///
    /// Implementations differ here and the difference is visible in the
    /// ranking, so both this and the Go reference do it the same way and both
    /// say so.
    fn page_rank(&self, iterations: usize, damping: f64, top: usize) -> Vec<i64> {
        let n = self.nodes();
        if n == 0 {
            return Vec::new();
        }
        let mut rank = vec![1.0 / n as f64; n];
        let mut next = vec![0.0f64; n];

        for _ in 0..iterations {
            let mut dangling = 0.0f64;
            next.iter_mut().for_each(|v| *v = 0.0);
            for (i, r) in rank.iter().enumerate() {
                let out = self.row(i as u32);
                if out.is_empty() {
                    dangling += r;
                    continue;
                }
                let share = r / out.len() as f64;
                for m in out {
                    next[*m as usize] += share;
                }
            }
            let base = (1.0 - damping) / n as f64 + damping * dangling / n as f64;
            next.iter_mut().for_each(|v| *v = base + damping * *v);
            std::mem::swap(&mut rank, &mut next);
        }

        let mut order: Vec<u32> = (0..n as u32).collect();
        // Ties are broken by the lower identifier, which is arbitrary and is at
        // least the same arbitrary choice in every implementation.
        order.sort_by(|a, b| {
            let (x, y) = (rank[*a as usize], rank[*b as usize]);
            y.partial_cmp(&x)
                .unwrap_or(std::cmp::Ordering::Equal)
                .then_with(|| self.ids[*a as usize].cmp(&self.ids[*b as usize]))
        });
        order
            .into_iter()
            .take(top.min(n))
            .map(|i| self.ids[i as usize] as i64)
            .collect()
    }
}

fn store_path(work: &Path) -> PathBuf {
    work.join("graph.kgrf")
}

fn read_u32(f: &mut impl Read, count: usize) -> std::io::Result<Vec<u32>> {
    let mut raw = vec![0u8; count * 4];
    f.read_exact(&mut raw)?;
    Ok(raw
        .chunks_exact(4)
        .map(|c| u32::from_le_bytes([c[0], c[1], c[2], c[3]]))
        .collect())
}

fn read_u64(f: &mut impl Read, count: usize) -> std::io::Result<Vec<u64>> {
    let mut raw = vec![0u8; count * 8];
    f.read_exact(&mut raw)?;
    Ok(raw
        .chunks_exact(8)
        .map(|c| u64::from_le_bytes(c.try_into().unwrap()))
        .collect())
}
