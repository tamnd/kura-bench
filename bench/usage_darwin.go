//go:build darwin

package bench

import "runtime"

// Darwin reports ru_maxrss in bytes. See the note in usage_linux.go.
const maxrssUnit = 1

// memory returns the peak and current resident set.
//
// There is no cheap equivalent of the Linux status file here, so the current
// figure is the Go heap rather than the process resident set. It is the smaller
// of the two and it is labelled as an approximation in the results, because
// quietly reporting a heap number in a column headed resident memory would make
// this platform look better than the others for no reason.
func memory() (peak, current int64) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return peakRSS(), int64(ms.HeapAlloc)
}

// ioBytes has no per process equivalent on this platform that does not need
// elevated privileges, so it reports nothing rather than guessing.
func ioBytes() (read, write int64) { return 0, 0 }
