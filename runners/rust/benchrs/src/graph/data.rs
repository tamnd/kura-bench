//! Reading the three files a prepared graph consists of.
//!
//! The edge file and the seed file are fixed width binary, written once by
//! kura-graphs so that no runner ever parses a gzipped tab separated list. The
//! format is described in the Go package that writes it: eight magic bytes,
//! the node and edge counts as little endian u64, the largest identifier and a
//! flag word as u32, and then the edges as pairs of little endian u32 in the
//! order the publisher wrote them.

use std::collections::HashMap;
use std::fs::File;
use std::io::{self, BufReader, Read};
use std::path::Path;

use serde::Deserialize;

const MAGIC: &[u8; 8] = b"kuragrf1";
const HEADER: usize = 32;

/// UNDIRECTED marks a graph the publisher stored with both directions of every
/// edge written out.
pub const UNDIRECTED: u32 = 1 << 0;

/// What the front of an edge file says about it.
#[derive(Debug, Clone, Copy, Default)]
pub struct Header {
    /// How many distinct identifiers appear, on either side of an edge.
    pub nodes: usize,

    /// How many edge records follow.
    pub edges: usize,

    /// The largest identifier. The gap between it and `nodes` is worth
    /// knowing: web-Google has 875,713 nodes and identifiers up to 916,428, so
    /// a store that indexes an array by identifier wastes five percent.
    pub max_id: u32,

    /// Carries [`UNDIRECTED`].
    pub flags: u32,
}

impl Header {
    /// Whether the publisher wrote both directions of every edge.
    pub fn undirected(&self) -> bool {
        self.flags & UNDIRECTED != 0
    }
}

/// Reads the header without reading the edges, which is what a check before a
/// run needs.
///
/// A file that is short is the failure worth catching. It happens when a
/// conversion was killed partway, and every figure taken from it afterwards is
/// slightly optimistic and completely useless.
pub fn header(path: &Path) -> io::Result<Header> {
    let mut f = File::open(path)?;
    let size = f.metadata()?.len();

    let mut head = [0u8; HEADER];
    f.read_exact(&mut head)?;
    if &head[0..8] != MAGIC {
        return Err(bad(format!(
            "{} does not start with kuragrf1, so it is not an edge file",
            path.display()
        )));
    }

    let h = Header {
        nodes: u64::from_le_bytes(head[8..16].try_into().unwrap()) as usize,
        edges: u64::from_le_bytes(head[16..24].try_into().unwrap()) as usize,
        max_id: u32::from_le_bytes(head[24..28].try_into().unwrap()),
        flags: u32::from_le_bytes(head[28..32].try_into().unwrap()),
    };

    let want = HEADER as u64 + h.edges as u64 * 8;
    if size != want {
        return Err(bad(format!(
            "{} is {size} bytes, a graph of {} edges is {want}",
            path.display(),
            h.edges
        )));
    }
    Ok(h)
}

/// Reads the whole edge file, from and to alternating.
///
/// It is one flat allocation because that is the layout every store here wants
/// to consume. LiveJournal is sixty nine million edges, and handing them over
/// as a vector of pairs would measure the allocator.
///
/// The whole file is read every time. A machine that cannot hold the graph
/// prepares a smaller one with kura-graphs, which writes a real subgraph with
/// its own answers, rather than reading part of a file and comparing what it
/// finds against answers worked out on the rest of it too.
pub fn edges(path: &Path) -> io::Result<(Header, Vec<u32>)> {
    let h = header(path)?;
    let count = h.edges;

    let mut f = BufReader::with_capacity(1 << 20, File::open(path)?);
    let mut skip = [0u8; HEADER];
    f.read_exact(&mut skip)?;

    let mut raw = vec![0u8; count * 8];
    f.read_exact(&mut raw)?;

    let mut out = Vec::with_capacity(count * 2);
    for chunk in raw.chunks_exact(4) {
        out.push(u32::from_le_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]));
    }
    Ok((h, out))
}

/// Reads a list of node identifiers, which is what the seed file is.
pub fn seeds(path: &Path) -> io::Result<Vec<u32>> {
    let raw = std::fs::read(path)?;
    if raw.len() % 4 != 0 {
        return Err(bad(format!(
            "{} is {} bytes, which is not a whole number of identifiers",
            path.display(),
            raw.len()
        )));
    }
    Ok(raw
        .chunks_exact(4)
        .map(|c| u32::from_le_bytes([c[0], c[1], c[2], c[3]]))
        .collect())
}

/// How much of each operation a run does.
///
/// This is the Go package's Plan, read back rather than reimplemented, because
/// the answers were worked out under exactly these numbers and a runner that
/// invented its own would be checking a different question's answers.
#[derive(Debug, Clone, Copy, Default, Deserialize)]
pub struct Plan {
    pub seeds: usize,
    pub neighbour: usize,
    #[serde(rename = "two_hop")]
    pub two_hop: usize,
    pub path: usize,
    pub bfs: usize,
    pub iterations: usize,
    pub damping: f64,
    pub top: usize,
}

/// What the operations should come back with.
///
/// Every operation reduces to a list of whole numbers on purpose. It makes a
/// disagreement a comparison rather than a schema, and it is the same three
/// lines of code in every runner in every language.
///
/// - neighbours, one out degree per seed
/// - two-hop, one distinct count per seed, not counting the seed
/// - shortest-path, one hop count per pair, -1 when there is no path
/// - bfs, two per seed, the reachable count then the depth
/// - pagerank, the highest ranked node identifiers, best first
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Answers {
    pub nodes: usize,
    pub edges: usize,
    pub plan: Plan,
    pub answers: HashMap<String, Vec<i64>>,
}

impl Answers {
    /// The expected vector for one operation, or an empty slice when the
    /// answers file does not have that operation in it.
    pub fn get(&self, op: &str) -> &[i64] {
        self.answers.get(op).map(Vec::as_slice).unwrap_or(&[])
    }
}

/// Reads the answers file.
pub fn answers(path: &Path) -> io::Result<Answers> {
    let raw = std::fs::read(path)?;
    let a: Answers =
        serde_json::from_slice(&raw).map_err(|e| bad(format!("{}: {e}", path.display())))?;
    if a.answers.is_empty() {
        return Err(bad(format!("{}: no answers in it", path.display())));
    }
    Ok(a)
}

fn bad(message: String) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn tempdir(name: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("benchrs-graph-{name}"));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        dir
    }

    fn write_edges(dir: &Path, pairs: &[(u32, u32)], flags: u32) -> std::path::PathBuf {
        let path = dir.join("edges.bin");
        let mut f = File::create(&path).unwrap();
        let mut seen = std::collections::HashSet::new();
        let mut max_id = 0u32;
        for (a, b) in pairs {
            seen.insert(*a);
            seen.insert(*b);
            max_id = max_id.max(*a).max(*b);
        }
        f.write_all(MAGIC).unwrap();
        f.write_all(&(seen.len() as u64).to_le_bytes()).unwrap();
        f.write_all(&(pairs.len() as u64).to_le_bytes()).unwrap();
        f.write_all(&max_id.to_le_bytes()).unwrap();
        f.write_all(&flags.to_le_bytes()).unwrap();
        for (a, b) in pairs {
            f.write_all(&a.to_le_bytes()).unwrap();
            f.write_all(&b.to_le_bytes()).unwrap();
        }
        path
    }

    #[test]
    fn it_reads_an_edge_file() {
        let dir = tempdir("read");
        let path = write_edges(&dir, &[(1, 2), (2, 3), (3, 1)], UNDIRECTED);

        let (h, e) = edges(&path).unwrap();
        assert_eq!(h.nodes, 3);
        assert_eq!(h.edges, 3);
        assert_eq!(h.max_id, 3);
        assert!(h.undirected());
        assert_eq!(e, vec![1, 2, 2, 3, 3, 1]);
    }

    /// A file that was written by something else, or by an older version of
    /// this one, is refused rather than read as noise.
    #[test]
    fn a_file_that_is_not_an_edge_file_is_refused() {
        let dir = tempdir("magic");
        let path = dir.join("edges.bin");
        std::fs::write(&path, vec![0u8; 64]).unwrap();
        assert!(header(&path).is_err());
    }

    /// A conversion that was killed partway leaves a file that reads perfectly
    /// and describes a graph that is missing its tail.
    #[test]
    fn a_short_file_is_refused() {
        let dir = tempdir("short");
        let path = write_edges(&dir, &[(1, 2), (2, 3)], 0);
        let full = std::fs::read(&path).unwrap();
        std::fs::write(&path, &full[..full.len() - 4]).unwrap();
        assert!(header(&path).is_err());
    }

    #[test]
    fn it_reads_the_answers_and_the_plan_out_of_one_file() {
        let dir = tempdir("answers");
        let path = dir.join("answers.json");
        std::fs::write(
            &path,
            br#"{"nodes":3,"edges":3,"plan":{"seeds":2,"neighbour":2,"two_hop":1,
                "path":1,"bfs":1,"iterations":20,"damping":0.85,"top":2},
                "answers":{"neighbours":[1,1],"bfs":[3,2]}}"#,
        )
        .unwrap();

        let a = answers(&path).unwrap();
        assert_eq!(a.nodes, 3);
        assert_eq!(a.plan.two_hop, 1);
        assert_eq!(a.plan.damping, 0.85);
        assert_eq!(a.get("neighbours"), &[1, 1]);
        assert!(a.get("pagerank").is_empty());
    }
}
