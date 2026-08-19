//go:build darwin || linux

package bench

import (
	"syscall"
	"time"
)

// cpuTime returns processor time in and out of the kernel for this process.
func cpuTime() (user, sys time.Duration) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0
	}
	return timeval(ru.Utime), timeval(ru.Stime)
}

func timeval(tv syscall.Timeval) time.Duration {
	return time.Duration(tv.Sec)*time.Second + time.Duration(tv.Usec)*time.Microsecond
}

// peakRSS returns the high water mark of resident memory from getrusage, which
// reports the peak rather than the current figure. That is the number deciding
// how much memory a machine has to have to run the job at all, and it cannot be
// recovered from a sample taken afterwards because by then the allocator has
// usually handed some of it back.
func peakRSS() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return int64(ru.Maxrss) * maxrssUnit
}
