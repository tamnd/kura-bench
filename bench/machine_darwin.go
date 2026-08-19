//go:build darwin

package bench

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
)

func cpuModel() string { return sysctl("machdep.cpu.brand_string") }

func memoryTotals() (total, free int64) {
	total, _ = strconv.ParseInt(sysctl("hw.memsize"), 10, 64)
	// There is no single counter for available memory here that is worth the
	// arithmetic it would take to assemble, so this reports the total only. The
	// load average is the field that catches a busy machine anyway.
	return total, 0
}

func loadAverage() float64 {
	fields := strings.Fields(strings.Trim(sysctl("vm.loadavg"), "{}"))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return v
}

// sysctl reads one value through the shipped tool.
//
// The syscall wrappers for these differ by value type and one of them is not
// exported on every version of the standard library, which is more special
// cases than a field used to label a results table is worth.
func sysctl(name string) string {
	out, err := exec.CommandContext(context.Background(), "sysctl", "-n", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
