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

	// LoadBefore and LoadAfter are the one minute load average at each end of
	// the phase.
	//
	// [Machine.LoadBefore] already records what the machine was doing when the
	// run started, and on a machine to ourselves that is enough. On a shared one
	// it is not, because a full run takes fifteen minutes and the one minute
	// average has turned over fifteen times by the end of it. An index phase
	// that ran alone and a query phase that ran against eleven other people
	// produce one result file, and until these fields existed nothing in it said
	// so. The first engine in a run and the last engine in a run are the pair
	// this most often separates.
	//
	// Zero means not recorded rather than an idle machine. Windows has no run
	// queue average to read, and a phase that ran while the machine was doing
	// nothing at all still reports a small positive number for the process doing
	// the measuring.
	LoadBefore float64 `json:"load_before,omitempty"`
	LoadAfter  float64 `json:"load_after,omitempty"`
}

// Contended says the phase ran on a machine that was busy with something else.
//
// The threshold is per core for the same reason [Describe] uses one: a load of
// four is nothing on thirty two cores and is the machine falling over on two.
// The higher of the two ends is taken, because a phase that started idle and
// finished under load was still measured against that load for part of its
// life, and the number it produced is a floor either way.
//
// Cores of zero means the caller does not know how many there are, and this
// answers false rather than guessing.
func (u Usage) Contended(cores int) bool {
	if cores <= 0 {
		return false
	}
	load := u.LoadBefore
	if u.LoadAfter > load {
		load = u.LoadAfter
	}
	// A phase that recorded nothing is not a phase that recorded an idle
	// machine, so it does not get to claim it was uncontended.
	if load == 0 {
		return false
	}
	return load/float64(cores) >= 0.2
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
	load  float64
}

// Take reads the counters now. Pair it with [Measure].
//
// The clock is read last, so everything it costs to read the other counters
// falls before the interval starts rather than inside it. That ordering used to
// be a rounding error and is not any more: the load average on macOS comes from
// a subprocess, and the cold start phase it would otherwise be charged to is
// twenty one milliseconds long.
func Take() Snapshot {
	load := loadAverage()
	user, sys := cpuTime()
	read, write := ioBytes()
	return Snapshot{
		at:    time.Now(),
		user:  user,
		sys:   sys,
		read:  read,
		write: write,
		load:  load,
	}
}

// takeAtEnd is [Take] with the clock read first, which is the ordering that
// closes an interval rather than opening one.
func takeAtEnd() Snapshot {
	at := time.Now()
	user, sys := cpuTime()
	read, write := ioBytes()
	return Snapshot{
		at:    at,
		user:  user,
		sys:   sys,
		read:  read,
		write: write,
		load:  loadAverage(),
	}
}

// Measure returns the usage since a snapshot.
//
// Processor time and input and output are differences, so they describe the
// phase. Memory is not, for the reason given on [Usage.PeakRSSBytes].
func Measure(start Snapshot) Usage {
	now := takeAtEnd()
	peak, rss := memory()
	return Usage{
		WallSeconds:  now.at.Sub(start.at).Seconds(),
		UserSeconds:  (now.user - start.user).Seconds(),
		SysSeconds:   (now.sys - start.sys).Seconds(),
		PeakRSSBytes: peak,
		RSSBytes:     rss,
		ReadBytes:    now.read - start.read,
		WriteBytes:   now.write - start.write,
		LoadBefore:   start.load,
		LoadAfter:    now.load,
	}
}
