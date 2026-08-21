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

#[derive(Serialize, Default)]
pub struct SearchPhase {
    pub usage: Usage,
    pub queries: Vec<QueryStat>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub concurrent: Option<ConcurrentStat>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub residency: Option<Residency>,
}

/// What the query set touched of a mapped index.
///
/// Every count is optional because a platform that will not answer has to look
/// different from one that answered zero. See the Go side of this contract for
/// what the fields mean and why the distinction matters.
#[derive(Serialize, Default)]
pub struct Residency {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub faults: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub faults_from_disk: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub resident_before: Option<u64>,
    pub total: u64,
    pub page: u64,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub note: String,
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
