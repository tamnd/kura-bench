//go:build windows

package bench

import (
	"os"
	"unsafe"
)

// memoryStatusEx is MEMORYSTATUSEX from sysinfoapi.h.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")

// cpuModel comes from the environment, which Windows fills in with the
// processor identifier. It is coarser than the brand string the other platforms
// report and it is what is available without a management query.
func cpuModel() string {
	if v := os.Getenv("PROCESSOR_IDENTIFIER"); v != "" {
		return v
	}
	return os.Getenv("PROCESSOR_ARCHITECTURE")
}

func memoryTotals() (total, free int64) {
	var s memoryStatusEx
	s.Length = uint32(unsafe.Sizeof(s))
	r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return 0, 0
	}
	return int64(s.TotalPhys), int64(s.AvailPhys)
}

// loadAverage has no equivalent here. Windows has no run queue average, and the
// nearest thing is a performance counter that has to be sampled over a window
// before it means anything. Reporting zero is honest; the free memory figure
// and the operator's own judgement cover the case this was for.
func loadAverage() float64 { return 0 }
