//! Reading the corpus file.
//!
//! It streams rather than collecting, because the corpus is larger than the
//! heap this engine wants to be measured with and a runner that held the whole
//! thing in memory before indexing it would be measuring something nobody
//! deploys.

use serde::Deserialize;
use std::fs::File;
use std::io::{BufRead, BufReader};
use std::path::Path;

#[derive(Deserialize, Clone)]
pub struct Document {
    pub id: String,
    pub repo: String,
    pub path: String,
    pub title: String,
    pub body: String,
    pub ext: String,
}

/// Calls `f` for every document, and stops early when it returns false.
pub fn read<F>(name: &Path, mut f: F) -> std::io::Result<()>
where
    F: FnMut(Document) -> bool,
{
    let file = File::open(name)?;
    // A megabyte of buffer, because the documents are up to that size and a
    // smaller one turns a sequential read into a syscall per document.
    let reader = BufReader::with_capacity(1 << 20, file);
    for line in reader.lines() {
        let line = line?;
        if line.is_empty() {
            continue;
        }
        let doc: Document = serde_json::from_str(&line)
            .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))?;
        if !f(doc) {
            return Ok(());
        }
    }
    Ok(())
}

/// Reads the query file, ignoring blank lines and comments.
pub fn queries(name: &Path) -> std::io::Result<Vec<String>> {
    let text = std::fs::read_to_string(name)?;
    Ok(text
        .lines()
        .map(str::trim)
        .filter(|l| !l.is_empty() && !l.starts_with('#'))
        .map(str::to_string)
        .collect())
}
