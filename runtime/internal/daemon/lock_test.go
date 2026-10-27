package daemon

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestSingleInstanceLockUnderContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners, losers := 0, 0
	locks := make(chan *Lock, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := TryLock(path)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners++
				locks <- l
			case errors.Is(err, ErrAlreadyRunning):
				losers++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if winners != 1 || losers != 19 {
		t.Fatalf("R1: exactly one instance, got winners=%d losers=%d", winners, losers)
	}
	(<-locks).Unlock()
	l, err := TryLock(path)
	if err != nil {
		t.Fatalf("lock must be re-acquirable after release: %v", err)
	}
	l.Unlock()
}
