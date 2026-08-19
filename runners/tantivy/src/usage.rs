//! What one phase cost.
//!
//! Wall clock alone is not enough to compare these engines. Tantivy indexes on
//! every core it is given, and on a machine with thirty two threads that is a
//! wall clock number several times better than a single threaded engine doing
//! the same work, while costing more processor time in total. A deployment pays
//! for the processor time, so both are reported.

use serde::Serialize;
use std::time::Instant;

#[derive(Serialize, Default, Clone)]
pub struct Usage {
    pub wall_seconds: f64,
    pub user_seconds: f64,
    pub sys_seconds: f64,

    /// The high water mark of resident memory for the whole process up to the
    /// end of this phase. The operating system keeps only the maximum and it
    /// never goes down, so a later phase inherits the peak of every phase
    /// before it. That is the honest reading of the number available, and it is
    /// still the one that decides how much memory the machine has to have.
    pub peak_rss_bytes: i64,
    pub rss_bytes: i64,
    pub read_bytes: i64,
    pub write_bytes: i64,
}

/// A reading of the process counters at one instant.
pub struct Snapshot {
    at: Instant,
    user: f64,
    sys: f64,
    read: i64,
    write: i64,
}

pub fn take() -> Snapshot {
    let (user, sys) = cpu_time();
    let (read, write) = io_bytes();
    Snapshot {
        at: Instant::now(),
        user,
        sys,
        read,
        write,
    }
}

/// Usage since a snapshot. Processor time and input and output are differences
/// so they describe the phase. Memory is not, for the reason on the field.
pub fn measure(start: &Snapshot) -> Usage {
    let now = take();
    let (peak, rss) = memory();
    Usage {
        wall_seconds: now.at.duration_since(start.at).as_secs_f64(),
        user_seconds: now.user - start.user,
        sys_seconds: now.sys - start.sys,
        peak_rss_bytes: peak,
        rss_bytes: rss,
        read_bytes: now.read - start.read,
        write_bytes: now.write - start.write,
    }
}

#[cfg(unix)]
mod platform {
    /// Linux reports the peak in kilobytes and macOS in bytes. Getting this
    /// wrong is a factor of a thousand, which is large enough to look like a
    /// finding rather than a bug.
    #[cfg(target_os = "linux")]
    const MAXRSS_UNIT: i64 = 1024;
    #[cfg(not(target_os = "linux"))]
    const MAXRSS_UNIT: i64 = 1;

    fn rusage() -> libc::rusage {
        let mut ru: libc::rusage = unsafe { std::mem::zeroed() };
        unsafe {
            libc::getrusage(libc::RUSAGE_SELF, &mut ru);
        }
        ru
    }

    pub fn cpu_time() -> (f64, f64) {
        let ru = rusage();
        let secs = |tv: libc::timeval| tv.tv_sec as f64 + tv.tv_usec as f64 / 1e6;
        (secs(ru.ru_utime), secs(ru.ru_stime))
    }

    pub fn memory() -> (i64, i64) {
        // The cast is not redundant everywhere. The field is a long, which is
        // already 64 bits on the platforms this is usually built for and is not
        // on all of them.
        #[allow(clippy::unnecessary_cast)]
        let peak = rusage().ru_maxrss as i64 * MAXRSS_UNIT;

        // Linux publishes the current resident size, and macOS does not offer
        // it without asking the Mach kernel, so there the peak is reported
        // twice rather than an invented current figure.
        #[cfg(target_os = "linux")]
        {
            if let Some(v) = status_field("VmRSS:") {
                return (peak, v);
            }
        }
        (peak, peak)
    }

    #[cfg(target_os = "linux")]
    fn status_field(prefix: &str) -> Option<i64> {
        let text = std::fs::read_to_string("/proc/self/status").ok()?;
        for line in text.lines() {
            if let Some(rest) = line.strip_prefix(prefix) {
                let kb: i64 = rest.trim().trim_end_matches(" kB").trim().parse().ok()?;
                return Some(kb * 1024);
            }
        }
        None
    }

    pub fn io_bytes() -> (i64, i64) {
        #[cfg(target_os = "linux")]
        {
            if let Ok(text) = std::fs::read_to_string("/proc/self/io") {
                let mut read = 0;
                let mut write = 0;
                for line in text.lines() {
                    if let Some(v) = line.strip_prefix("read_bytes:") {
                        read = v.trim().parse().unwrap_or(0);
                    }
                    if let Some(v) = line.strip_prefix("write_bytes:") {
                        write = v.trim().parse().unwrap_or(0);
                    }
                }
                return (read, write);
            }
        }
        // macOS has no per process byte counter that does not need elevated
        // privileges, so this is zero and the field name says only that.
        (0, 0)
    }
}

#[cfg(windows)]
mod platform {
    use windows_sys::Win32::Foundation::FILETIME;
    use windows_sys::Win32::System::ProcessStatus::{
        GetProcessMemoryInfo, PROCESS_MEMORY_COUNTERS,
    };
    use windows_sys::Win32::System::Threading::{
        GetCurrentProcess, GetProcessIoCounters, GetProcessTimes, IO_COUNTERS,
    };

    pub fn cpu_time() -> (f64, f64) {
        let mut creation = FILETIME::default();
        let mut exit = FILETIME::default();
        let mut kernel = FILETIME::default();
        let mut user = FILETIME::default();
        let ok = unsafe {
            GetProcessTimes(
                GetCurrentProcess(),
                &mut creation,
                &mut exit,
                &mut kernel,
                &mut user,
            )
        };
        if ok == 0 {
            return (0.0, 0.0);
        }
        (seconds(user), seconds(kernel))
    }

    /// A file time is a count of hundred nanosecond intervals.
    fn seconds(ft: FILETIME) -> f64 {
        let ticks = ((ft.dwHighDateTime as u64) << 32) | ft.dwLowDateTime as u64;
        ticks as f64 / 1e7
    }

    pub fn memory() -> (i64, i64) {
        let mut c = PROCESS_MEMORY_COUNTERS::default();
        c.cb = std::mem::size_of::<PROCESS_MEMORY_COUNTERS>() as u32;
        let ok = unsafe { GetProcessMemoryInfo(GetCurrentProcess(), &mut c, c.cb) };
        if ok == 0 {
            return (0, 0);
        }
        (c.PeakWorkingSetSize as i64, c.WorkingSetSize as i64)
    }

    pub fn io_bytes() -> (i64, i64) {
        let mut c = IO_COUNTERS::default();
        let ok = unsafe { GetProcessIoCounters(GetCurrentProcess(), &mut c) };
        if ok == 0 {
            return (0, 0);
        }
        (c.ReadTransferCount as i64, c.WriteTransferCount as i64)
    }
}

use platform::{cpu_time, io_bytes, memory};
