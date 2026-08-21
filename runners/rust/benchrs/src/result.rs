//! The JSON a runner writes.
//!
//! These structs exist twice, once here and once in the Go package that defines
//! the contract. That is the price of a contract that is a pipe rather than a
//! language interface, and it is worth paying: the alternative is that every
//! engine measured here has to be written in Go, which rules out most of the
//! ones worth measuring.

use serde::Serialize;

use crate::machine::Machine;
use crate::usage::Usage;

#[derive(Serialize, Default)]
pub struct Result {
    pub engine: String,
    pub version: String,
    pub language: String,
    pub corpus: CorpusStats,
    pub index: IndexPhase,
    pub open: OpenPhase,
    pub search: SearchPhase,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub update: Option<UpdatePhase>,
    pub machine: Machine,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub notes: String,
}

#[derive(Serialize, Default)]
pub struct CorpusStats {
    pub documents: usize,
    pub bytes: i64,
}

#[derive(Serialize, Default)]
pub struct IndexPhase {
    pub usage: Usage,
    pub bytes: i64,
    pub files: usize,
}

#[derive(Serialize, Default)]
pub struct OpenPhase {
    pub usage: Usage,
    pub resident_bytes: i64,
}

fn is_zero(n: &usize) -> bool {
    *n == 0
}

#[derive(Serialize, Default)]
pub struct SearchPhase {
    pub usage: Usage,

    /// How many results each search asked for. A latency at a page of ten and a
    /// latency at a page of a hundred are different measurements, and recall
    /// computed over these pages is recall at this number and no other, so two
    /// result files that disagree here are not comparable.
    #[serde(skip_serializing_if = "is_zero")]
    pub depth: usize,

    pub queries: Vec<QueryStat>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub concurrent: Option<ConcurrentStat>,
}

#[derive(Serialize, Default)]
pub struct UpdatePhase {
    pub usage: Usage,
    pub documents: usize,
    pub bytes: i64,
    pub index_bytes_after: i64,
}

#[derive(Serialize, Default)]
pub struct QueryStat {
    pub query: String,
    pub hits: usize,
    pub runs: usize,
    pub min_ms: f64,
    pub median_ms: f64,
    pub p90_ms: f64,
    pub p99_ms: f64,
    pub max_ms: f64,

    /// The page the engine returned, in the order it returned it, collected on
    /// the warm up run so that no timed run pays for it.
    ///
    /// A relevance score needs to know what came back and not only how many,
    /// and a latency comparison needs a way to check that two engines answered
    /// the same question. The total does not establish that on its own.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub ids: Vec<String>,
}

#[derive(Serialize, Default)]
pub struct ConcurrentStat {
    pub workers: usize,
    pub queries: usize,
    pub seconds: f64,
    pub median_ms: f64,
    pub p99_ms: f64,
}

/// Summarises a set of timings in milliseconds.
///
/// It discards nothing. Which runs to throw away is a decision that belongs
/// where a reader can see it, not in a helper every engine shares.
pub fn summarise(query: &str, hits: usize, mut ms: Vec<f64>) -> QueryStat {
    if ms.is_empty() {
        return QueryStat {
            query: query.to_string(),
            hits,
            ..Default::default()
        };
    }
    ms.sort_by(|a, b| a.partial_cmp(b).unwrap());
    QueryStat {
        query: query.to_string(),
        hits,
        runs: ms.len(),
        min_ms: ms[0],
        median_ms: percentile(&ms, 0.50),
        p90_ms: percentile(&ms, 0.90),
        p99_ms: percentile(&ms, 0.99),
        max_ms: ms[ms.len() - 1],
        // The caller fills this in, because which run the page came off is the
        // caller's decision and not this helper's.
        ids: Vec::new(),
    }
}

/// Nearest rank on an already sorted slice, which is the definition that does
/// not invent a value nobody measured.
pub fn percentile(sorted: &[f64], p: f64) -> f64 {
    if sorted.is_empty() {
        return 0.0;
    }
    let i = (p * sorted.len() as f64).ceil() as isize - 1;
    let i = i.clamp(0, sorted.len() as isize - 1) as usize;
    sorted[i]
}

/// Adds up the files under a path and counts them. A path that does not exist
/// is not an error, because an engine that keeps nothing on disk is a
/// legitimate answer.
pub fn dir_size(dir: &std::path::Path) -> (i64, usize) {
    let mut total = 0i64;
    let mut files = 0usize;
    let Ok(entries) = std::fs::read_dir(dir) else {
        return (0, 0);
    };
    for entry in entries.flatten() {
        let Ok(meta) = entry.metadata() else { continue };
        if meta.is_dir() {
            let (t, f) = dir_size(&entry.path());
            total += t;
            files += f;
        } else {
            total += meta.len() as i64;
            files += 1;
        }
    }
    (total, files)
}
