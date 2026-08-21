package bench

import (
	"runtime"
	"testing"
	"time"
)

func TestContentionIsJudgedPerCore(t *testing.T) {
	busy := Usage{LoadBefore: 4, LoadAfter: 4}
	if !busy.Contended(2) {
		t.Error("a load of four on two cores is a busy machine")
	}
	if busy.Contended(32) {
		t.Error("a load of four on thirty two cores is an idle machine")
	}
}

// A phase that started idle and finished under load was still measured against
// that load for part of its life, so the number it produced is a floor.
func TestAPhaseThatGotBusyHalfwayThroughIsContended(t *testing.T) {
	u := Usage{LoadBefore: 0.4, LoadAfter: 19.7}
	if !u.Contended(8) {
		t.Error("a phase that ended at a load of nineteen was not measured on an idle machine")
	}
}

// Zero is what a platform with no run queue average reports and what a result
// written before the field existed carries, and neither is a claim that the
// machine was idle.
func TestAPhaseThatRecordedNoLoadDoesNotClaimToBeIdleOrBusy(t *testing.T) {
	if (Usage{}).Contended(8) {
		t.Error("an unrecorded load was read as a busy machine")
	}
	if (Usage{LoadBefore: 4, LoadAfter: 4}).Contended(0) {
		t.Error("contention was judged without knowing how many cores there are")
	}
}

// The whole point of the field is that it describes the phase rather than the
// run, so it has to come off the two ends of the interval and not off one.
func TestMeasureRecordsTheLoadAtBothEndsOfThePhase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no run queue average to read")
	}
	start := Take()
	u := Measure(start)
	if u.LoadBefore <= 0 {
		t.Errorf("no load recorded at the start of the phase, got %v", u.LoadBefore)
	}
	if u.LoadAfter <= 0 {
		t.Errorf("no load recorded at the end of the phase, got %v", u.LoadAfter)
	}
}

// Reading the load costs a subprocess on macOS, measured at one and a half to
// three milliseconds, and the cold start phase it would otherwise be charged to
// is twenty one milliseconds long. An instrument that changes what it measures
// is worse than no instrument, so the reads sit outside the interval.
//
// A phase that does nothing should therefore measure as nothing. The minimum
// over many attempts is what gets checked, because on a shared machine any
// single attempt can be preempted for longer than the cost being guarded
// against, and no amount of slack fixes that without making the check vacuous.
// The minimum is not affected by a scheduler taking one of them.
func TestReadingTheCountersIsNotChargedToThePhase(t *testing.T) {
	lowest := time.Hour
	for range 50 {
		start := Take()
		u := Measure(start)
		if took := time.Duration(u.WallSeconds * float64(time.Second)); took < lowest {
			lowest = took
		}
	}
	// The counters themselves are eleven microseconds. This is well above that
	// and well below the millisecond and a half the load average costs.
	if lowest > 200*time.Microsecond {
		t.Errorf("a phase that did nothing measured %v, so reading the counters is inside the interval", lowest)
	}
}
