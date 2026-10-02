package proposal

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentAddsAndSingleAccept(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() { _, _ = Add(dir, "memory-trash", nil, "r") })
	}
	wg.Wait()
	all := Load(dir)
	if len(all) != 40 {
		t.Fatalf("lost proposals: %d of 40", len(all))
	}
	var ok atomic.Int32
	for range 5 {
		wg.Go(func() {
			if _, err := Decide(dir, all[0].ID, Pending, Accepted); err == nil {
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("accepted %d times", ok.Load())
	}
}

func TestPendingSuggestionsAreCapped(t *testing.T) {
	dir := t.TempDir()
	for i := range MaxPending {
		if _, err := Add(dir, "memory-trash", map[string]any{"path": "/x"}, "r"); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, err := Add(dir, "memory-trash", nil, "r"); !errors.Is(err, ErrTooMany) {
		t.Fatalf("suggestion %d was accepted: %v", MaxPending+1, err)
	}
}
