//! Reading the corpus file.
//!
//! It streams rather than collecting, because the corpus is larger than the
//! heap this engine wants to be measured with and a runner that held the whole
//! thing in memory before indexing it would be measuring something nobody
//! deploys.

use serde::Deserialize;
use std::fs::File;
use std::io::{BufRead, BufReader, Seek, SeekFrom};
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

/// Splits the corpus into `count` byte ranges of roughly equal size.
///
/// The cuts land wherever the arithmetic puts them and not on a line boundary.
/// [`read_range`] is what makes them line up: a range starts at the first line
/// that begins after its start, and runs past its end to finish the line it is
/// in, so every document belongs to exactly one range and no document is split.
///
/// A count above the number of lines gives empty ranges rather than an error,
/// which is what a small corpus and a large machine should do.
pub fn shards(name: &Path, count: usize) -> std::io::Result<Vec<(u64, u64)>> {
    let len = std::fs::metadata(name)?.len();
    let count = count.max(1) as u64;
    Ok((0..count)
        .map(|i| (len * i / count, len * (i + 1) / count))
        .collect())
}

/// Calls `f` for every document whose line begins inside `[from, to)`.
///
/// This is how a runner reads the corpus on more than one thread. Each thread
/// opens the file itself and reads its own range, so the parsing goes as wide as
/// the indexing does. A runner that parsed on one thread and fanned out from
/// there would be measuring the parser.
pub fn read_range<F>(name: &Path, from: u64, to: u64, mut f: F) -> std::io::Result<()>
where
    F: FnMut(Document) -> bool,
{
    let mut file = File::open(name)?;
    let mut at = from.saturating_sub(u64::from(from > 0));
    file.seek(SeekFrom::Start(at))?;
    let mut reader = BufReader::with_capacity(1 << 20, file);
    let mut line = Vec::new();
    if from > 0 {
        // The line this range starts in the middle of belongs to the range
        // before it, which reads past its own end to finish it. Starting a byte
        // early is what makes a range that begins exactly on a line boundary
        // keep that line rather than let both ranges skip it.
        at += reader.read_until(b'\n', &mut line)? as u64;
    }
    while at < to {
        line.clear();
        let read = reader.read_until(b'\n', &mut line)?;
        if read == 0 {
            break;
        }
        at += read as u64;
        let text = line.strip_suffix(b"\n").unwrap_or(&line);
        if text.is_empty() {
            continue;
        }
        let doc: Document = serde_json::from_slice(text)
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

#[cfg(test)]
mod tests {
    use super::*;

    /// The property every parallel runner rests on: the ranges together are the
    /// corpus, in order, with nothing read twice and nothing missed.
    #[test]
    fn the_shards_together_are_the_corpus() {
        let dir = std::env::temp_dir().join("benchrs-shards");
        std::fs::create_dir_all(&dir).expect("makes the directory");
        let path = dir.join("corpus.jsonl");
        let mut text = String::new();
        for i in 0..500 {
            text.push_str(&format!(
                "{{\"id\":\"{i}\",\"repo\":\"r\",\"path\":\"p\",\"title\":\"t\",\"body\":\"{}\",\"ext\":\"md\"}}\n",
                "word ".repeat(i % 40)
            ));
        }
        std::fs::write(&path, &text).expect("writes the corpus");

        let mut whole = Vec::new();
        read(&path, |d| {
            whole.push(d.id);
            true
        })
        .expect("reads");
        assert_eq!(whole.len(), 500);

        for count in [1, 2, 3, 7, 64, 700] {
            let mut parts = Vec::new();
            for (from, to) in shards(&path, count).expect("splits") {
                read_range(&path, from, to, |d| {
                    parts.push(d.id);
                    true
                })
                .expect("reads a range");
            }
            assert_eq!(parts, whole, "{count} shards");
        }
        std::fs::remove_dir_all(&dir).ok();
    }
}
