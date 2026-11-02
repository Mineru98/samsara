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

func TestRecordClaudeNeverCarriesVerdictAcrossVersions(t *testing.T) {
	yes, no := true, false
	m := Manifest{ClaudeCodeVersion: "2.1.300", PolicyHookFailureBlockVerified: true}
	m.RecordClaude("2.1.316", nil)
	if m.PolicyHookFailureBlockVerified || m.ClaudeCodeVersion != "2.1.316" {
		t.Fatalf("verdict for 2.1.300 carried over to 2.1.316: %+v", m)
	}
	m.RecordClaude("2.1.316", &yes)
	if !m.PolicyHookFailureBlockVerified {
		t.Fatal("passing probe must record true")
	}
	m.RecordClaude("2.1.316", nil)
	if !m.PolicyHookFailureBlockVerified {
		t.Fatal("re-writing the manifest for the same version must keep the verdict")
	}
	m.RecordClaude("2.1.316", &no)
	if m.PolicyHookFailureBlockVerified {
		t.Fatal("failing probe must record false")
	}
	m.RecordClaude("", &yes)
	if m.PolicyHookFailureBlockVerified {
		t.Fatal("an unknown Claude version is never verified")
	}
}
