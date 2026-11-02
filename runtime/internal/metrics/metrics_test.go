package metrics

import (
	"sync"
	"testing"
	"time"
)

func TestSummaryPercentilesAndWindow(t *testing.T) {
	var s Summary
	for i := 1; i <= 100; i++ {
		s.Observe(time.Duration(i) * time.Millisecond)
	}
	v := s.View()
	if v.Count != 100 || v.P50Ms != 50 || v.P95Ms != 95 || v.P99Ms != 99 || v.MaxMs != 100 {
		t.Fatalf("view = %+v", v)
	}
	for i := 0; i < 2*window; i++ {
		s.Observe(time.Millisecond)
	}
	v = s.View()
	if v.Window != window || v.Count != 100+2*window || v.P99Ms != 1 || v.MaxMs != 100 {
		t.Fatalf("window view = %+v", v)
	}
}

func TestCounterConcurrent(t *testing.T) {
	var c Counter
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Inc("LEASE_REVOKED")
			}
		}()
	}
	wg.Wait()
	if got := c.View()["LEASE_REVOKED"]; got != 800 {
		t.Fatalf("count = %d", got)
	}
}
