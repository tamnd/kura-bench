package bench

import "time"

// Usage is what one phase cost.
//
// Wall clock alone is not enough to compare these engines. Several of them
// index on every core they can find, and on a machine with thirty two threads
// that turns into a wall clock number three times better than an engine doing
// the same work on one core, while costing ten times the processor time. A
// deployment pays for the processor time. Both are reported, and the ratio
// between them is the parallelism the engine actually achieved.
type Usage struct {
	// WallSeconds is elapsed time.
	WallSeconds float64 `json:"wall_seconds"`

	// UserSeconds and SysSeconds are processor time in and out of the kernel.
	// A large system share usually means the engine is making a syscall per
	// document, which is a fixable thing and worth seeing separately.
	UserSeconds float64 `json:"user_seconds"`
	SysSeconds  float64 `json:"sys_seconds"`

	// PeakRSSBytes is the high water mark of resident memory for the whole
	// process up to the end of this phase.
	//
	// It is a process wide maximum rather than a per phase one, because the
	// operating system only keeps the maximum and it never goes down. A later
	// phase therefore inherits the peak of every phase before it. That is the
	// honest reading of the number the kernel offers, and it is still the one
	// that decides how much memory the machine has to have.
	PeakRSSBytes int64 `json:"peak_rss_bytes"`

	// RSSBytes is resident memory at the end of the phase, which is the figure
	// an idle process settles at.
	RSSBytes int64 `json:"rss_bytes"`

	// ReadBytes and WriteBytes are what the process moved to and from storage
	// during the phase. They are zero on platforms that do not offer them
	// rather than an assertion that no input or output happened, which is why
	// the field names do not promise more than that.
	ReadBytes  int64 `json:"read_bytes"`
	WriteBytes int64 `json:"write_bytes"`
}

// CPUSeconds is total processor time.
func (u Usage) CPUSeconds() float64 { return u.UserSeconds + u.SysSeconds }

// Parallelism is processor seconds per wall second.
//
// One means the phase ran on a single core the whole time. Eight means it kept
// eight busy. Well under one means it was waiting on storage, which for an
// indexing phase is the most useful thing this whole struct can tell you.
func (u Usage) Parallelism() float64 {
	if u.WallSeconds <= 0 {
		return 0
	}
	return u.CPUSeconds() / u.WallSeconds
}

// Snapshot is a reading of the process counters at one instant.
type Snapshot struct {
	at    time.Time
	user  time.Duration
	sys   time.Duration
	read  int64
	write int64
}

// Take reads the counters now. Pair it with [Measure].
func Take() Snapshot {
	user, sys := cpuTime()
	read, write := ioBytes()
	return Snapshot{at: time.Now(), user: user, sys: sys, read: read, write: write}
}

// Measure returns the usage since a snapshot.
//
// Processor time and input and output are differences, so they describe the
// phase. Memory is not, for the reason given on [Usage.PeakRSSBytes].
func Measure(start Snapshot) Usage {
	now := Take()
	peak, rss := memory()
	return Usage{
		WallSeconds:  now.at.Sub(start.at).Seconds(),
		UserSeconds:  (now.user - start.user).Seconds(),
		SysSeconds:   (now.sys - start.sys).Seconds(),
		PeakRSSBytes: peak,
		RSSBytes:     rss,
		ReadBytes:    now.read - start.read,
		WriteBytes:   now.write - start.write,
	}
}
