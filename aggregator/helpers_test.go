package aggregator

import "testing"

func newTestAggregator(t *testing.T, cfg Config) *Aggregator {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}
