//! The JSON a graph runner writes.
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
pub struct GraphResult {
    pub engine: String,
    pub version: String,
    pub language: String,
    pub dataset: GraphStats,
    pub build: GraphBuildPhase,
    pub open: OpenPhase,
    pub query: GraphQueryPhase,
    pub machine: Machine,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub notes: String,
}

/// The graph as the runner read it off disk, rather than as the flags claimed.
#[derive(Serialize, Default)]
pub struct GraphStats {
    pub name: String,
    pub nodes: usize,
    pub edges: usize,

    /// Whether the publisher stored both directions of every edge. It changes
    /// what a traversal means: a reachable set on an undirected graph is a
    /// connected component and on a directed one it is not.
    pub undirected: bool,

    /// How many nodes the run asked about.
    pub seeds: usize,
}

#[derive(Serialize, Default)]
pub struct GraphBuildPhase {
    pub usage: Usage,
    pub bytes: i64,
    pub files: usize,
}

#[derive(Serialize, Default)]
pub struct GraphQueryPhase {
    /// Every operation together, which is where the peak resident figure comes
    /// from. Per operation CPU is not broken out because a traversal and a
    /// point lookup share a process and there is no honest way to split it.
    pub usage: Usage,

    /// The operations, cheapest first.
    pub ops: Vec<OpStat>,
}

/// One operation, run as many times as the plan said.
#[derive(Serialize, Default)]
pub struct OpStat {
    /// The operation's name, spelled the way the Go package spells it.
    pub op: String,

    /// How many times it ran, which is one for pagerank and a thousand for a
    /// neighbour lookup.
    pub runs: usize,

    /// The fraction of answers that matched the ground truth. One means every
    /// answer agreed with an implementation written in another language, which
    /// is the only reason any of the timings below are worth reading.
    pub correct: f64,

    /// How many disagreed, which is the number somebody debugging wants and is
    /// not recoverable from the fraction once it has been rounded.
    pub mismatches: usize,

    pub min_ms: f64,
    pub median_ms: f64,
    pub p90_ms: f64,
    pub p99_ms: f64,
    pub max_ms: f64,

    /// The same operation with several in flight, which is the throughput a
    /// server would see and is not one over the serial latency.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub concurrent: Option<ConcurrentStat>,

    /// Why this operation has no timings, for a store that cannot do it. An
    /// empty row with a reason in it is worth more than a missing row, which
    /// reads as an oversight.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub unsupported: String,
}

/// Summarises one operation from its timings in milliseconds.
///
/// It discards nothing. Which runs to throw away is a decision that belongs
/// where a reader can see it, not in a helper every engine shares.
pub fn summarise(op: &str, correct: f64, mismatches: usize, mut ms: Vec<f64>) -> OpStat {
    if ms.is_empty() {
        return OpStat {
            op: op.to_string(),
            correct,
            mismatches,
            ..Default::default()
        };
    }
    ms.sort_by(|a, b| a.partial_cmp(b).unwrap());
    OpStat {
        op: op.to_string(),
        runs: ms.len(),
        correct,
        mismatches,
        min_ms: ms[0],
        median_ms: percentile(&ms, 0.50),
        p90_ms: percentile(&ms, 0.90),
        p99_ms: percentile(&ms, 0.99),
        max_ms: ms[ms.len() - 1],
        concurrent: None,
        unsupported: String::new(),
    }
}

/// An operation the store cannot do, recorded with the reason.
pub fn unsupported(op: &str, why: &str) -> OpStat {
    OpStat {
        op: op.to_string(),
        unsupported: why.to_string(),
        ..Default::default()
    }
}
