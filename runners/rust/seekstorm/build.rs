//! Stamps the resolved SeekStorm version into the binary.
//!
//! The version a table wants is the engine's, not this crate's, and SeekStorm
//! does not expose its own at run time the way Tantivy does. Cargo has already
//! written the resolved version into the workspace lock file, so that is where
//! it is read from. Reading it here rather than writing it out by hand means a
//! bumped dependency cannot quietly leave a stale number in a published result.

use std::path::Path;

fn main() {
    let manifest = std::env::var("CARGO_MANIFEST_DIR").expect("cargo sets the manifest directory");
    let lock = Path::new(&manifest).join("..").join("Cargo.lock");
    println!("cargo:rerun-if-changed={}", lock.display());

    let text =
        std::fs::read_to_string(&lock).unwrap_or_else(|e| panic!("read {}: {e}", lock.display()));
    let version = locked_version(&text, "seekstorm")
        .unwrap_or_else(|| panic!("no seekstorm entry in {}", lock.display()));
    println!("cargo:rustc-env=SEEKSTORM_VERSION={version}");
}

/// Finds the version cargo resolved for one package.
///
/// A lock file is a list of `[[package]]` blocks, each with a name and a
/// version, so the answer is the version line that follows the matching name
/// line. Parsing it with a TOML crate would pull a build dependency in for six
/// lines of work.
fn locked_version(lock: &str, package: &str) -> Option<String> {
    let want = format!("name = \"{package}\"");
    let mut lines = lock.lines().map(str::trim);
    while let Some(line) = lines.next() {
        if line != want {
            continue;
        }
        for line in lines.by_ref() {
            if let Some(rest) = line.strip_prefix("version = ") {
                return Some(rest.trim_matches('"').to_string());
            }
            if line.starts_with("[[") {
                break;
            }
        }
    }
    None
}
