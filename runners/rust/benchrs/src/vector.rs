//! The measuring half of a Rust vector runner.
//!
//! Same argument as the text suite. Reading the vectors, scoring the recall,
//! parsing the flags and writing the result are identical between engines
//! because otherwise the comparison includes the reading, the scoring and the
//! parsing, and a benchmark where each engine brings its own recall function is
//! not a benchmark.
//!
//! A vector runner is therefore an index and nothing else: it reads a
//! [`config::Config`], builds or opens an index over [`data::fvecs`], searches
//! it, and fills in a [`result::VectorResult`].

pub mod config;
pub mod data;
pub mod result;
pub mod search;
