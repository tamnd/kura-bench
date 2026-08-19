//go:build linux

package bench

import (
	"os"
	"strconv"
	"strings"
)

// Linux reports ru_maxrss in kilobytes. Darwin reports it in bytes. Getting
// this wrong is a factor of a thousand in a column meant to be compared across
// machines, so it is a named constant per platform rather than a conversion
// somebody has to remember at the call site.
const maxrssUnit = 1024

// memory returns the peak and current resident set.
//
// The current figure comes from the status file rather than from getrusage,
// which has no field for it.
func memory() (peak, current int64) {
	peak = peakRSS()
	if v, ok := statusField("VmRSS:"); ok {
		current = v * 1024
	}
	return peak, current
}

// ioBytes returns bytes this process has read from and written to storage.
//
// These are the counters that see through the page cache, so a phase that
// looks fast because everything was already cached shows a read figure far
// below the bytes it processed. That difference is worth having: a benchmark
// where it is near zero is measuring memory, not storage.
func ioBytes() (read, write int64) {
	b, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "read_bytes":
			read = n
		case "write_bytes":
			write = n
		}
	}
	return read, write
}

func statusField(prefix string) (int64, bool) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}
