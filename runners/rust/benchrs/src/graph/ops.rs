//! The five operations, and the loop that times them.
//!
//! This is here rather than in each runner for the same reason the vector
//! suite's search loop is. An engine that timed its own loop could stop the
//! clock before it had counted the neighbours it walked, or start it after the
//! adjacency row was already in cache, and nobody reading the table would be
//! able to tell. Every engine in the graph suite goes through [`run`], so the
//! only thing that differs between two rows is what happens inside the
//! [`Engine`] methods.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

use crate::graph::config::Config;
use crate::graph::data::Answers;
use crate::graph::result::{OpStat, summarise, unsupported};
use crate::result::{ConcurrentStat, percentile};

/// One hop out of one node, the cheapest thing a graph store does and the one
/// it does most.
pub const NEIGHBOURS: &str = "neighbours";

/// The distinct nodes within two hops, which is where the cost of a hub shows
/// up.
pub const TWO_HOP: &str = "two-hop";

/// The hop count between two nodes, or nothing when they are not connected.
pub const SHORTEST_PATH: &str = "shortest-path";

/// The whole reachable set from one node, the operation that touches
/// everything and cannot be helped by any index.
pub const BFS: &str = "bfs";

/// The whole graph, several times over, which is the analytics workload rather
/// than the serving one.
pub const PAGERANK: &str = "pagerank";

/// The operations in the order the report shows them, cheapest first.
pub const ALL: [&str; 5] = [NEIGHBOURS, TWO_HOP, SHORTEST_PATH, BFS, PAGERANK];

/// How many runs an operation needs before it is also measured with several in
/// flight.
///
/// Below this the concurrent figure is mostly the cost of starting the threads.
/// It leaves the traversals and PageRank serial, which is right: nobody runs
/// ten breadth first searches at once to find out how fast one is.
const CONCURRENT_MIN: usize = 100;

/// A built graph store, whatever engine is behind it.
///
/// Every method takes and returns the publisher's own node identifiers, not
/// whatever dense index the store settled on internally. A store that has its
/// own numbering maps back inside the call, which means the mapping is timed,
/// which is correct: an answer the caller cannot read has not been produced.
pub trait Engine: Sync {
    /// Why this store cannot do an operation, or None when it can.
    ///
    /// It is a sentence for the report rather than a boolean, because a row
    /// that says why it is empty is worth more than a row that is missing, and
    /// a missing row reads as an oversight.
    fn cannot(&self, _op: &str) -> Option<String> {
        None
    }

    /// The out degree of one node.
    fn neighbours(&self, node: u32) -> i64;

    /// The distinct nodes within two hops of one node, not counting the node
    /// itself.
    fn two_hop(&self, node: u32) -> i64;

    /// The shortest path length in hops, or -1 when there is no path.
    fn shortest_path(&self, from: u32, to: u32) -> i64;

    /// The size of the reachable set from one node, and how deep it goes.
    fn bfs(&self, node: u32) -> (i64, i64);

    /// The highest ranked node identifiers, best first.
    ///
    /// The iteration count and the damping come from the plan rather than from
    /// the engine, because a PageRank figure without them is not a measurement
    /// of anything, and two engines that chose their own would be running
    /// different amounts of work.
    fn page_rank(&self, iterations: usize, damping: f64, top: usize) -> Vec<i64>;
}

/// Runs every operation the plan asks for and scores it against the answers.
///
/// The seeds are taken from the front of the list in the order they were
/// written, so a run with fewer of them is a subset of a run with more rather
/// than a different sample, and two engines are always asked about the same
/// nodes in the same order.
pub fn run(engine: &dyn Engine, cfg: &Config, seeds: &[u32], answers: &Answers) -> Vec<OpStat> {
    let plan = answers.plan;
    let mut out = Vec::new();

    for op in ALL {
        if !cfg.wants(op) {
            continue;
        }
        if let Some(why) = engine.cannot(op) {
            out.push(unsupported(op, &why));
            continue;
        }
        let want = answers.get(op);
        let workers = cfg.worker_count();

        out.push(match op {
            NEIGHBOURS => {
                let n = plan.neighbour.min(seeds.len());
                let mut stat = one_per_seed(op, want, n, |i| engine.neighbours(seeds[i]));
                stat.concurrent = alongside(n, workers, |i| engine.neighbours(seeds[i]));
                stat
            }
            TWO_HOP => {
                let n = plan.two_hop.min(seeds.len());
                let mut stat = one_per_seed(op, want, n, |i| engine.two_hop(seeds[i]));
                stat.concurrent = alongside(n, workers, |i| engine.two_hop(seeds[i]));
                stat
            }
            SHORTEST_PATH => {
                let n = plan.path.min(seeds.len() / 2);
                let mut stat = one_per_seed(op, want, n, |i| {
                    engine.shortest_path(seeds[2 * i], seeds[2 * i + 1])
                });
                stat.concurrent = alongside(n, workers, |i| {
                    engine.shortest_path(seeds[2 * i], seeds[2 * i + 1])
                });
                stat
            }
            BFS => traversals(op, want, plan.bfs.min(seeds.len()), seeds, engine),
            PAGERANK => {
                let start = Instant::now();
                let got = engine.page_rank(plan.iterations, plan.damping, plan.top);
                let ms = start.elapsed().as_secs_f64() * 1000.0;
                let (correct, wrong) = score(&got, want);
                summarise(op, correct, wrong, vec![ms])
            }
            _ => unreachable!(),
        });
    }
    out
}

/// Times an operation that produces one number per seed.
fn one_per_seed<F>(op: &str, want: &[i64], count: usize, call: F) -> OpStat
where
    F: Fn(usize) -> i64,
{
    let mut got = Vec::with_capacity(count);
    let mut ms = Vec::with_capacity(count);
    for i in 0..count {
        let start = Instant::now();
        let answer = call(i);
        ms.push(start.elapsed().as_secs_f64() * 1000.0);
        got.push(answer);
    }
    let (correct, wrong) = score(&got, want);
    summarise(op, correct, wrong, ms)
}

/// Times the traversals, which produce two numbers each.
fn traversals(op: &str, want: &[i64], count: usize, seeds: &[u32], engine: &dyn Engine) -> OpStat {
    let mut got = Vec::with_capacity(count * 2);
    let mut ms = Vec::with_capacity(count);
    for seed in seeds.iter().take(count) {
        let start = Instant::now();
        let (reached, depth) = engine.bfs(*seed);
        ms.push(start.elapsed().as_secs_f64() * 1000.0);
        got.push(reached);
        got.push(depth);
    }
    let (correct, wrong) = score(&got, want);
    summarise(op, correct, wrong, ms)
}

/// Runs the same operation again with several in flight.
///
/// The workers take seeds off a shared cursor rather than being handed a slice
/// each, because a two hop lookup on a hub costs a thousand times what one on a
/// leaf costs and a static split would end with one worker still going while
/// the rest idle, which reports as lower throughput than the store has.
fn alongside<F>(count: usize, workers: usize, call: F) -> Option<ConcurrentStat>
where
    F: Fn(usize) -> i64 + Sync,
{
    if count < CONCURRENT_MIN || workers == 0 {
        return None;
    }

    let cursor = AtomicUsize::new(0);
    let start = Instant::now();
    let mut ms: Vec<f64> = std::thread::scope(|scope| {
        let handles: Vec<_> = (0..workers)
            .map(|_| {
                let cursor = &cursor;
                let call = &call;
                scope.spawn(move || {
                    let mut mine = Vec::new();
                    loop {
                        let i = cursor.fetch_add(1, Ordering::Relaxed);
                        if i >= count {
                            return mine;
                        }
                        let at = Instant::now();
                        let _ = call(i);
                        mine.push(at.elapsed().as_secs_f64() * 1000.0);
                    }
                })
            })
            .collect();
        handles
            .into_iter()
            .flat_map(|h| h.join().unwrap_or_default())
            .collect()
    });
    let seconds = start.elapsed().as_secs_f64();

    ms.sort_by(|a, b| a.partial_cmp(b).unwrap());
    Some(ConcurrentStat {
        workers,
        queries: ms.len(),
        seconds,
        median_ms: percentile(&ms, 0.50),
        p99_ms: percentile(&ms, 0.99),
    })
}

/// The fraction of answers that matched, and how many did not.
///
/// An answer the ground truth has nothing to say about counts as wrong rather
/// than being skipped. That case means the answers file and the plan disagree
/// about how many of something to run, and quietly scoring out of the shorter
/// of the two would turn a broken run into a perfect score.
pub fn score(got: &[i64], want: &[i64]) -> (f64, usize) {
    if got.is_empty() {
        return (0.0, 0);
    }
    let mut wrong = 0usize;
    for (i, v) in got.iter().enumerate() {
        if want.get(i) != Some(v) {
            wrong += 1;
        }
    }
    let correct = (got.len() - wrong) as f64 / got.len() as f64;
    (correct, wrong)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_answer_agreeing_is_a_one() {
        assert_eq!(score(&[1, 2, 3], &[1, 2, 3]), (1.0, 0));
    }

    #[test]
    fn a_disagreement_is_counted_as_well_as_scored() {
        assert_eq!(score(&[1, 9, 3], &[1, 2, 3]), (2.0 / 3.0, 1));
    }

    /// A ground truth that is shorter than the run means the answers file and
    /// the plan disagree, and scoring out of the shorter of the two would turn
    /// that into a perfect score.
    #[test]
    fn answers_the_ground_truth_says_nothing_about_are_wrong() {
        assert_eq!(score(&[1, 2, 3], &[1, 2]), (2.0 / 3.0, 1));
    }

    #[test]
    fn nothing_run_is_not_scored_at_all() {
        assert_eq!(score(&[], &[1, 2]), (0.0, 0));
    }
}
