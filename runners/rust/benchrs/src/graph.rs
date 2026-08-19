//! The measuring half of a Rust graph runner.
//!
//! Same argument as the other two suites. Reading the edge file, picking the
//! seeds apart, timing the operations and checking the answers are identical
//! between engines because otherwise the comparison includes the reading, the
//! timing and the checking.
//!
//! A graph runner is therefore a store and nothing else: it reads a
//! [`config::Config`], loads the graph from [`data::edges`], implements
//! [`ops::Engine`], and hands it to [`ops::run`], which fills in a
//! [`result::GraphResult`].

pub mod config;
pub mod data;
pub mod ops;
pub mod result;
