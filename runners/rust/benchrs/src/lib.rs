//! The measuring half of a Rust runner.
//!
//! Every engine here is measured by the same code: the same corpus reader, the
//! same process counters, the same machine description, the same result shape
//! and the same flags. That is not tidiness, it is the whole point. Two engines
//! timed by two pieces of code are not being compared to each other, they are
//! being compared to their own stopwatches.
//!
//! A runner is therefore an engine and nothing else. It reads a [`config::Config`],
//! does the work, and fills in a [`result::Result`].

pub mod config;
pub mod corpus;
pub mod machine;
pub mod result;
pub mod usage;
pub mod vector;

/// How many documents the update phase rewrites. The Go runners use the same
/// number, on purpose: a figure that means five thousand documents on one
/// engine and a fifth of the corpus on another is not comparable.
pub const UPDATE_DOCUMENTS: usize = 5000;

/// How many results a search asks for. A result list is ten long, and an engine
/// asked for ten thousand is doing a different job.
pub const SEARCH_LIMIT: usize = 10;
