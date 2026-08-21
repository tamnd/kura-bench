//! Reading fvecs and ivecs files, and scoring an answer against ground truth.
//!
//! The format is as simple as it looks. A file is a sequence of records, each a
//! little endian i32 giving the number of components followed by that many f32
//! values, or i32 values for an ivecs file. There is no header, so the record
//! count is the file size divided by the size of the first record, which is
//! also the check that the file is what it claims to be.

use std::fs::File;
use std::io::{self, BufReader, Read};
use std::path::Path;

/// What a file turns out to hold.
#[derive(Debug, Clone, Copy, Default)]
pub struct Shape {
    /// Components in each record, read from the first one.
    pub dim: usize,

    /// How many records the file holds.
    pub count: usize,

    /// The size of the file.
    pub bytes: u64,
}

/// Reads the first record header and derives the rest from the file size,
/// without reading the body.
///
/// A file whose size is not a whole number of records is refused rather than
/// truncated. A vector file that is half downloaded reads perfectly well for
/// the first few hundred megabytes and then produces a recall figure that is
/// wrong for a reason nobody would ever guess.
pub fn read_shape(path: &Path, elem: usize) -> io::Result<Shape> {
    let mut f = File::open(path)?;
    let bytes = f.metadata()?.len();

    let mut head = [0u8; 4];
    f.read_exact(&mut head)?;
    let dim = i32::from_le_bytes(head);
    if dim <= 0 || dim > (1 << 20) {
        return Err(bad(format!(
            "{}: first record claims {dim} components, which is not a vector file",
            path.display()
        )));
    }

    let dim = dim as usize;
    let record = (4 + dim * elem) as u64;
    if bytes % record != 0 {
        return Err(bad(format!(
            "{}: {bytes} bytes is not a whole number of {dim} component records, the file is truncated",
            path.display()
        )));
    }
    Ok(Shape {
        dim,
        count: (bytes / record) as usize,
        bytes,
    })
}

/// Reads a float vector file into one flat vector of `count * dim` values.
///
/// It is one allocation because that is how every engine here wants the data.
/// A base set of a million vectors is half a gigabyte and handing it over as a
/// million small vectors would measure the allocator.
///
/// A `limit` above zero stops after that many records, which is how a machine
/// with less memory than the dataset still produces a row.
pub fn fvecs(path: &Path, limit: usize) -> io::Result<(Shape, Vec<f32>)> {
    let (shape, raw) = read(path, limit)?;
    let mut out = Vec::with_capacity(shape.count * shape.dim);
    // as_chunks gives back whole four byte arrays rather than slices, so
    // from_le_bytes takes them directly and there is no fallible conversion in
    // the loop that reads half a gigabyte.
    for chunk in raw.as_chunks::<4>().0 {
        out.push(f32::from_le_bytes(*chunk));
    }
    Ok((shape, out))
}

/// Reads an integer vector file, which is the shape ground truth comes in: one
/// row per query holding the identifiers of its true nearest neighbours, in
/// order.
pub fn ivecs(path: &Path, limit: usize) -> io::Result<(Shape, Vec<i32>)> {
    let (shape, raw) = read(path, limit)?;
    let mut out = Vec::with_capacity(shape.count * shape.dim);
    for chunk in raw.as_chunks::<4>().0 {
        out.push(i32::from_le_bytes(*chunk));
    }
    Ok((shape, out))
}

/// Reads the bodies of every record, dropping the per record dimension header.
///
/// The header is checked on every record rather than only the first. It costs
/// one comparison per record against a file that is being read anyway, and it
/// is the one check that catches a file which is the right length and the wrong
/// contents.
fn read(path: &Path, limit: usize) -> io::Result<(Shape, Vec<u8>)> {
    let mut shape = read_shape(path, 4)?;
    if limit > 0 && limit < shape.count {
        shape.count = limit;
    }

    let mut f = BufReader::with_capacity(1 << 20, File::open(path)?);
    let row = shape.dim * 4;
    let mut out = vec![0u8; shape.count * row];
    let mut head = [0u8; 4];
    for i in 0..shape.count {
        f.read_exact(&mut head)?;
        let dim = i32::from_le_bytes(head) as usize;
        if dim != shape.dim {
            return Err(bad(format!(
                "{}: record {i} has {dim} components, the first had {}",
                path.display(),
                shape.dim
            )));
        }
        f.read_exact(&mut out[i * row..(i + 1) * row])?;
    }
    Ok((shape, out))
}

/// The fraction of the true nearest neighbours an engine found.
///
/// Only the first `k` of each ground truth row counts, because an engine asked
/// for ten results cannot be marked down for missing the eleventh. Ties at the
/// boundary are not corrected for: two vectors at exactly the same distance
/// make the ordering arbitrary, and every engine here pays that the same way.
pub fn recall(got: &[i32], want: &[i32], k: usize, queries: usize) -> f64 {
    if k == 0 || queries == 0 || got.len() < queries * k {
        return 0.0;
    }
    let depth = want.len() / queries;
    if depth < k {
        return 0.0;
    }

    let mut found = 0usize;
    let mut truth: std::collections::HashSet<i32> = std::collections::HashSet::with_capacity(k);
    for q in 0..queries {
        truth.clear();
        truth.extend(&want[q * depth..q * depth + k]);
        for id in &got[q * k..(q + 1) * k] {
            if truth.contains(id) {
                found += 1;
            }
        }
    }
    found as f64 / (queries * k) as f64
}

fn bad(message: String) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn write(dir: &Path, name: &str, dim: usize, rows: &[&[f32]]) -> std::path::PathBuf {
        let path = dir.join(name);
        let mut f = File::create(&path).unwrap();
        for r in rows {
            f.write_all(&(dim as i32).to_le_bytes()).unwrap();
            for v in *r {
                f.write_all(&v.to_le_bytes()).unwrap();
            }
        }
        path
    }

    fn tempdir(name: &str) -> std::path::PathBuf {
        let dir = std::env::temp_dir().join(format!("benchrs-vector-{name}"));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        dir
    }

    #[test]
    fn it_reads_a_float_vector_file() {
        let dir = tempdir("read");
        let path = write(&dir, "base.fvecs", 3, &[&[1.0, 2.0, 3.0], &[4.0, 5.0, 6.0]]);

        let (shape, data) = fvecs(&path, 0).unwrap();
        assert_eq!(shape.dim, 3);
        assert_eq!(shape.count, 2);
        assert_eq!(data, vec![1.0, 2.0, 3.0, 4.0, 5.0, 6.0]);
    }

    #[test]
    fn a_limit_stops_early_without_reading_the_rest() {
        let dir = tempdir("limit");
        let path = write(&dir, "base.fvecs", 3, &[&[1.0, 2.0, 3.0], &[4.0, 5.0, 6.0]]);

        let (shape, data) = fvecs(&path, 1).unwrap();
        assert_eq!(shape.count, 1);
        assert_eq!(data, vec![1.0, 2.0, 3.0]);
    }

    /// A file that stopped halfway through a download reads perfectly for its
    /// first few hundred megabytes and then produces a recall number that is
    /// wrong for a reason nobody would guess.
    #[test]
    fn a_half_downloaded_file_is_refused() {
        let dir = tempdir("truncated");
        let path = write(&dir, "base.fvecs", 3, &[&[1.0, 2.0, 3.0], &[4.0, 5.0, 6.0]]);

        let full = std::fs::read(&path).unwrap();
        std::fs::write(&path, &full[..full.len() - 4]).unwrap();
        assert!(read_shape(&path, 4).is_err());
    }

    #[test]
    fn recall_counts_only_the_first_k_of_each_ground_truth_row() {
        let want = [1, 2, 3, 4, 5, 10, 20, 30, 40, 50];
        let got = [1, 2, 10, 30];
        assert_eq!(recall(&got, &want, 2, 2), 0.75);
    }

    #[test]
    fn perfect_and_empty_recall() {
        let want = [1, 2, 3, 4];
        assert_eq!(recall(&[1, 2, 3, 4], &want, 2, 2), 1.0);
        assert_eq!(recall(&[9, 9, 9, 9], &want, 2, 2), 0.0);
        assert_eq!(recall(&[], &want, 2, 2), 0.0);
    }
}
