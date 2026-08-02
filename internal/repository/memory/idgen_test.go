package memory_test

import (
	"context"
	"sync"
	"testing"

	"urlshortener/internal/repository/memory"
)

func TestCodeGen_Next_Unique(t *testing.T) {
	gen := memory.NewCodeGen(0)
	ctx := context.Background()

	const n = 1000
	seen := make(map[uint64]bool, n)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := gen.Next(ctx)
			if err != nil {
				t.Errorf("Next() unexpected error: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[id] {
				t.Errorf("Next() produced duplicate id %d", id)
			}
			seen[id] = true
		}()
	}
	wg.Wait()
}
