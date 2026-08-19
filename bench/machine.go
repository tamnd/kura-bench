package bench

import (
	"os"
	"runtime"
)

// Machine is where a result was taken.
//
// Every field here has changed a number by more than any code change in this
// repository has. A result without it is not a measurement, it is an anecdote.
type Machine struct {
	// Host is the name the machine answers to, so a result can be traced back
	// to the box it came from.
	Host string `json:"host"`

	OS   string `json:"os"`
	Arch string `json:"arch"`

	// CPU is the model string, and Cores the number of logical processors the
	// runtime can see. An engine that indexes on every core is measured very
	// differently on four of them than on thirty two.
	CPU   string `json:"cpu"`
	Cores int    `json:"cores"`

	// MemoryBytes is total physical memory. It matters even when a run fits,
	// because an engine that relies on the page cache holding the index looks
	// very good on a machine with room for it and falls apart on one without.
	MemoryBytes int64 `json:"memory_bytes"`

	// LoadBefore is the one minute load average when the run started, and
	// MemoryFreeBytes the memory available to it.
	//
	// These exist because of a run that reported every number three to four
	// times worse than the one before it, which looked like a serious
	// regression and was another process on the same machine. If a whole table
	// moves by one factor, the machine moved and not the code, and the only way
	// to see that afterwards is to have written down what the machine was
	// doing at the time.
	LoadBefore       float64 `json:"load_before"`
	MemoryFreeBytes  int64   `json:"memory_free_bytes"`
	Dedicated        bool    `json:"dedicated"`
	DedicatedComment string  `json:"dedicated_comment,omitempty"`
}

// Describe reads what it can about the machine. Fields the platform does not
// offer are left empty rather than filled with a guess.
func Describe() Machine {
	host, _ := os.Hostname()
	m := Machine{
		Host:  host,
		OS:    runtime.GOOS,
		Arch:  runtime.GOARCH,
		Cores: runtime.NumCPU(),
	}
	m.CPU = cpuModel()
	m.MemoryBytes, m.MemoryFreeBytes = memoryTotals()
	m.LoadBefore = loadAverage()

	// A machine under load is not a benchmark machine. The threshold is per
	// core, because a load of four means something different on four cores than
	// on thirty two.
	if m.Cores > 0 {
		m.Dedicated = m.LoadBefore/float64(m.Cores) < 0.2
	}
	if !m.Dedicated {
		m.DedicatedComment = "the machine was busy when the run started, so treat these numbers as a floor"
	}
	return m
}
