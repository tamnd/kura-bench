//! Where a result was taken.
//!
//! Every field here has moved a number by more than any code change has. A
//! result without it is not a measurement, it is an anecdote.

use serde::Serialize;

#[derive(Serialize, Default)]
pub struct Machine {
    pub host: String,
    pub os: String,
    pub arch: String,
    pub cpu: String,
    pub cores: usize,
    pub memory_bytes: i64,
    pub load_before: f64,
    pub memory_free_bytes: i64,
    pub dedicated: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub dedicated_comment: String,
}

pub fn describe() -> Machine {
    let cores = std::thread::available_parallelism()
        .map(|n| n.get())
        .unwrap_or(0);
    let (total, free) = memory_totals();
    let load = load_average();

    let mut m = Machine {
        host: hostname(),
        os: std::env::consts::OS.to_string(),
        arch: std::env::consts::ARCH.to_string(),
        cpu: cpu_model(),
        cores,
        memory_bytes: total,
        load_before: load,
        memory_free_bytes: free,
        ..Default::default()
    };

    // A machine under load is not a benchmark machine. The threshold is per
    // core, because a load of four means something different on four cores than
    // on thirty two.
    if cores > 0 {
        m.dedicated = load / (cores as f64) < 0.2;
    }
    if !m.dedicated {
        m.dedicated_comment =
            "the machine was busy when the run started, so treat these numbers as a floor"
                .to_string();
    }
    m
}

fn hostname() -> String {
    #[cfg(unix)]
    {
        if let Ok(name) = std::fs::read_to_string("/etc/hostname") {
            let name = name.trim();
            if !name.is_empty() {
                return name.to_string();
            }
        }
        if let Ok(out) = std::process::Command::new("hostname").output() {
            return String::from_utf8_lossy(&out.stdout).trim().to_string();
        }
        String::new()
    }
    #[cfg(windows)]
    {
        std::env::var("COMPUTERNAME").unwrap_or_default()
    }
}

#[cfg(target_os = "linux")]
fn cpu_model() -> String {
    let Ok(text) = std::fs::read_to_string("/proc/cpuinfo") else {
        return String::new();
    };
    for line in text.lines() {
        if let Some(rest) = line.strip_prefix("model name")
            && let Some((_, v)) = rest.split_once(':')
        {
            return v.trim().to_string();
        }
    }
    String::new()
}

#[cfg(target_os = "linux")]
fn memory_totals() -> (i64, i64) {
    let Ok(text) = std::fs::read_to_string("/proc/meminfo") else {
        return (0, 0);
    };
    let mut total = 0;
    let mut free = 0;
    for line in text.lines() {
        if let Some(v) = line.strip_prefix("MemTotal:") {
            total = kilobytes(v);
        }
        if let Some(v) = line.strip_prefix("MemAvailable:") {
            free = kilobytes(v);
        }
    }
    (total, free)
}

#[cfg(target_os = "linux")]
fn kilobytes(field: &str) -> i64 {
    field
        .trim()
        .trim_end_matches(" kB")
        .trim()
        .parse::<i64>()
        .unwrap_or(0)
        * 1024
}

#[cfg(target_os = "linux")]
fn load_average() -> f64 {
    let Ok(text) = std::fs::read_to_string("/proc/loadavg") else {
        return 0.0;
    };
    text.split_whitespace()
        .next()
        .and_then(|v| v.parse().ok())
        .unwrap_or(0.0)
}

/// macOS keeps all three of these behind sysctl, and shelling out to it is
/// clearer than three sets of raw bindings for values that are read once at
/// startup.
#[cfg(target_os = "macos")]
fn sysctl(name: &str) -> String {
    std::process::Command::new("sysctl")
        .args(["-n", name])
        .output()
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default()
}

#[cfg(target_os = "macos")]
fn cpu_model() -> String {
    sysctl("machdep.cpu.brand_string")
}

#[cfg(target_os = "macos")]
fn memory_totals() -> (i64, i64) {
    let total = sysctl("hw.memsize").parse().unwrap_or(0);
    // There is no single figure for available memory here that means what
    // MemAvailable means, so it is left out rather than approximated from the
    // free page count, which reads as almost nothing on a healthy machine.
    (total, 0)
}

#[cfg(target_os = "macos")]
fn load_average() -> f64 {
    // The value looks like "{ 1.83 2.02 2.11 }".
    sysctl("vm.loadavg")
        .split_whitespace()
        .nth(1)
        .and_then(|v| v.parse().ok())
        .unwrap_or(0.0)
}

#[cfg(windows)]
fn cpu_model() -> String {
    std::env::var("PROCESSOR_IDENTIFIER").unwrap_or_default()
}

#[cfg(windows)]
fn memory_totals() -> (i64, i64) {
    use windows_sys::Win32::System::SystemInformation::{GlobalMemoryStatusEx, MEMORYSTATUSEX};
    // The windows-sys structs are plain repr(C) declarations with no Default,
    // and the API contract for all of them is a zeroed struct with the size
    // field filled in.
    let mut s: MEMORYSTATUSEX = unsafe { std::mem::zeroed() };
    s.dwLength = std::mem::size_of::<MEMORYSTATUSEX>() as u32;
    let ok = unsafe { GlobalMemoryStatusEx(&mut s) };
    if ok == 0 {
        return (0, 0);
    }
    (s.ullTotalPhys as i64, s.ullAvailPhys as i64)
}

/// Windows has no run queue average, and the nearest thing is a performance
/// counter that has to be sampled over a window before it means anything.
/// Reporting zero is honest, and the free memory figure covers the case this
/// was for.
#[cfg(windows)]
fn load_average() -> f64 {
    0.0
}
