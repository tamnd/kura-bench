//! Running the query set against a built index, and timing it.
//!
//! This is here rather than in each runner for the same reason the corpus
//! reader is. An engine that timed its own loop could stop the clock before
//! collecting the identifiers, or start it after the query vector was already
//! in cache, and nobody reading the table would be able to tell. Every engine
//! in the vector suite goes through these two functions, so the only thing that
//! differs between two rows is what happens inside [`Search::nearest`].

use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

use crate::result::{ConcurrentStat, percentile};

/// A built index, whatever engine is behind it.
pub trait Search: Sync {
    /// The identifiers of the k nearest base vectors to one query, nearest
    /// first.
    ///
    /// The identifier is the vector's position in the base file, because that
    /// is what the published ground truth is written in terms of. An engine
    /// with its own idea of an identifier has to map back to that itself, and
    /// doing it inside this call means the mapping is timed, which is correct:
    /// a search that returns numbers the caller cannot use has not finished.
    fn nearest(&self, query: &[f32], k: usize) -> Vec<i32>;
}

/// What the query set produced.
pub struct Answers {
    /// The identifiers, k per query, laid out one query after another so that
    /// they can be scored against ground truth directly.
    pub ids: Vec<i32>,

    /// How long each query took, in milliseconds.
    pub ms: Vec<f64>,
}

/// Runs every query one at a time.
///
/// One at a time is the latency an interactive caller sees, and it is the only
/// way to time a single query at all. It is not the throughput a server would
/// get, which is what [`concurrent`] is for.
pub fn serial(index: &dyn Search, queries: &[f32], dim: usize, k: usize) -> Answers {
    let count = queries.len() / dim;
    let mut ids = Vec::with_capacity(count * k);
    let mut ms = Vec::with_capacity(count);

    for q in 0..count {
        let query = &queries[q * dim..(q + 1) * dim];
        let start = Instant::now();
        let got = index.nearest(query, k);
        ms.push(start.elapsed().as_secs_f64() * 1000.0);

        // A short answer is padded rather than dropped. An index that returned
        // eight neighbours when asked for ten has scored eight out of ten, and
        // silently scoring it out of eight would reward it for giving up.
        ids.extend_from_slice(&got[..got.len().min(k)]);
        ids.resize((q + 1) * k, -1);
    }
    Answers { ids, ms }
}

/// Runs the same query set with several queries in flight.
///
/// The workers take queries off a shared cursor rather than being handed a
/// slice each, because the queries are not equally expensive on a graph index
/// and a static split would end with one worker still going while the rest
/// idle, which reports as lower throughput than the engine has.
pub fn concurrent(
    index: &dyn Search,
    queries: &[f32],
    dim: usize,
    k: usize,
    workers: usize,
) -> ConcurrentStat {
    let count = queries.len() / dim;
    if count == 0 || workers == 0 {
        return ConcurrentStat::default();
    }

    let cursor = AtomicUsize::new(0);
    let start = Instant::now();
    let mut ms: Vec<f64> = std::thread::scope(|scope| {
        let handles: Vec<_> = (0..workers)
            .map(|_| {
                let cursor = &cursor;
                scope.spawn(move || {
                    let mut mine = Vec::new();
                    loop {
                        let q = cursor.fetch_add(1, Ordering::Relaxed);
                        if q >= count {
                            return mine;
                        }
                        let query = &queries[q * dim..(q + 1) * dim];
                        let at = Instant::now();
                        let _ = index.nearest(query, k);
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
    ConcurrentStat {
        workers,
        queries: ms.len(),
        seconds,
        median_ms: percentile(&ms, 0.50),
        p99_ms: percentile(&ms, 0.99),
    }
}
