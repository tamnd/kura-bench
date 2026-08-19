//go:build windows

package bench

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processMemoryCounters is PROCESS_MEMORY_COUNTERS from psapi.h. Only two
// fields are read, but the whole struct has to be declared because the call is
// told its size and checks it.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// ioCounters is IO_COUNTERS from winbase.h.
type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	psapi                = windows.NewLazySystemDLL("psapi.dll")
	procMemoryInfo       = psapi.NewProc("GetProcessMemoryInfo")
	procGetProcessIoInfo = kernel32.NewProc("GetProcessIoCounters")
)

// memory returns the peak and current working set, which is the Windows name
// for resident memory.
func memory() (peak, current int64) {
	var c processMemoryCounters
	c.CB = uint32(unsafe.Sizeof(c))
	r, _, _ := procMemoryInfo.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&c)),
		uintptr(c.CB),
	)
	if r == 0 {
		return 0, 0
	}
	return int64(c.PeakWorkingSetSize), int64(c.WorkingSetSize)
}

// cpuTime returns processor time in and out of the kernel for this process.
func cpuTime() (user, sys time.Duration) {
	var creation, exit, kernel, userTime windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(),
		&creation, &exit, &kernel, &userTime); err != nil {
		return 0, 0
	}
	// A file time is a count of hundred nanosecond intervals.
	return filetime(userTime), filetime(kernel)
}

func filetime(ft windows.Filetime) time.Duration {
	ticks := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	return time.Duration(ticks) * 100 * time.Nanosecond
}

// ioBytes returns bytes this process has transferred. The counters include
// every handle rather than only files on disk, which is noted where they are
// reported.
func ioBytes() (read, write int64) {
	var c ioCounters
	r, _, _ := procGetProcessIoInfo.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&c)),
	)
	if r == 0 {
		return 0, 0
	}
	return int64(c.ReadTransferCount), int64(c.WriteTransferCount)
}
