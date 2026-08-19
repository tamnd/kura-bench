//! The JSON a vector runner writes.
//!
//! These structs exist twice, once here and once in the Go package that defines
//! the contract. That is the price of a contract that is a pipe rather than a
//! language interface, and it is worth paying: the alternative is that every
//! engine measured here has to be written in Go, which rules out most of the
//! ones worth measuring.

use serde::Serialize;

use crate::machine::Machine;
use crate::result::{ConcurrentStat, OpenPhase, percentile};
use crate::usage::Usage;

#[derive(Serialize, Default)]
pub struct VectorResult {
    pub engine: String,
    pub version: String,
    pub language: String,
    pub dataset: DatasetStats,
    pub build: BuildPhase,
    pub open: OpenPhase,
    pub search: VectorSearchPhase,
    pub machine: Machine,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub notes: String,
}

#[derive(Serialize, Default)]
pub struct DatasetStats {
    pub name: String,
    pub dim: usize,
    pub vectors: usize,
    pub queries: usize,
    pub k: usize,

    /// The distance the engine was told to use. An index built for cosine and
    /// scored against Euclidean ground truth gives a recall figure that is
    /// wrong in a way no timing will reveal, so it is written down.
    pub metric: String,
}

#[derive(Serialize, Default)]
pub struct BuildPhase {
    pub usage: Usage,
    pub bytes: i64,
    pub files: usize,
}

#[derive(Serialize, Default)]
pub struct VectorSearchPhase {
    pub usage: Usage,
    pub points: Vec<VectorPoint>,
}

/// The query set at one setting of whatever knob the engine exposes.
///
/// An exact engine has one point, its parameters are empty and its recall is
/// one by construction. Anything approximate has several, and the pair of
/// recall and throughput is the result. Either number on its own is not.
#[derive(Serialize, Default)]
pub struct VectorPoint {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub params: String,
    pub recall: f64,
    pub queries: usize,
    pub min_ms: f64,
    pub median_ms: f64,
    pub p90_ms: f64,
    pub p99_ms: f64,
    pub max_ms: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub concurrent: Option<ConcurrentStat>,

    /// The index this point searched, and what building it cost. Both stay zero
    /// for the usual engine, where every point is one index searched harder or
    /// less hard and the size belongs in the storage table.
    ///
    /// An engine whose setting is fixed at build time fills them in, because its
    /// operating points are different indexes rather than different ways of
    /// searching one, and a single size printed next to three recalls would hide
    /// the trade being made.
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub bytes: i64,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub build_seconds: f64,
}

fn is_zero_i64(v: &i64) -> bool {
    *v == 0
}

fn is_zero_f64(v: &f64) -> bool {
    *v == 0.0
}

/// Summarises one operating point from its timings in milliseconds.
///
/// It discards nothing. Which runs to throw away is a decision that belongs
/// where a reader can see it, not in a helper every engine shares.
pub fn summarise(params: &str, recall: f64, mut ms: Vec<f64>) -> VectorPoint {
    if ms.is_empty() {
        return VectorPoint {
            params: params.to_string(),
            recall,
            ..Default::default()
        };
    }
    ms.sort_by(|a, b| a.partial_cmp(b).unwrap());
    VectorPoint {
        params: params.to_string(),
        recall,
        queries: ms.len(),
        min_ms: ms[0],
        median_ms: percentile(&ms, 0.50),
        p90_ms: percentile(&ms, 0.90),
        p99_ms: percentile(&ms, 0.99),
        max_ms: ms[ms.len() - 1],
        concurrent: None,
        bytes: 0,
        build_seconds: 0.0,
    }
}
