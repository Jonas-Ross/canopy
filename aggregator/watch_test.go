package aggregator

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestWatch_DebouncesBurstEvents(t *testing.T) {
	const burst = 5
	const delay = 100 * time.Millisecond

	var fires atomic.Int64
	d := newDebouncer(delay, func(string) { fires.Add(1) })
	t.Cleanup(d.stop)

	// Burst: 5 rapid trips for the same key, well inside the debounce
	// window. Expect zero fires before the window elapses.
	for i := 0; i < burst; i++ {
		d.trip("/repo/wt-a")
	}
	if got := fires.Load(); got != 0 {
		t.Fatalf("during burst: fires=%d; want 0 (debounce in flight)", got)
	}

	// Wait past the trailing-edge window (with slack for scheduling).
	time.Sleep(delay + 200*time.Millisecond)
	if got := fires.Load(); got != 1 {
		t.Fatalf("after window: fires=%d; want exactly 1 collapsed fire", got)
	}

	// A subsequent trip after the window must fire on its own.
	d.trip("/repo/wt-a")
	time.Sleep(delay + 200*time.Millisecond)
	if got := fires.Load(); got != 2 {
		t.Errorf("after second trip: fires=%d; want 2", got)
	}
}

func TestWatch_DebouncerKeysAreIndependent(t *testing.T) {
	var fires atomic.Int64
	d := newDebouncer(100*time.Millisecond, func(string) { fires.Add(1) })
	t.Cleanup(d.stop)

	d.trip("/a")
	d.trip("/b")
	d.trip("/c")
	time.Sleep(300 * time.Millisecond)
	if got := fires.Load(); got != 3 {
		t.Errorf("distinct keys: fires=%d; want 3", got)
	}
}

func TestWatch_DebouncerStopCancelsPending(t *testing.T) {
	var fires atomic.Int64
	d := newDebouncer(100*time.Millisecond, func(string) { fires.Add(1) })
	d.trip("/a")
	d.stop()
	time.Sleep(200 * time.Millisecond)
	if got := fires.Load(); got != 0 {
		t.Errorf("after stop: fires=%d; want 0 (stop cancels)", got)
	}
}
