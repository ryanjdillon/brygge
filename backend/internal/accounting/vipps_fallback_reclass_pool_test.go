package accounting

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// The charge transaction runs every statement on its own connection, so
// applies queued on one charge row finish on a pool smaller than the queue.
func TestFallbackReclassConcurrentApplyOnSmallPool(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(2))
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &fallbackFixture{autoAssignFixture: &autoAssignFixture{svc: svc, club: club}}
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, club.ID)
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
	f.parked(t, 80000, "Pool Payer")
	targets := targetsOf(f.preview(t))
	if len(targets) != 1 {
		t.Fatalf("targets = %v", targets)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	results := make([]*VippsFallbackReclassResult, 4)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.ApplyVippsFallbackReclass(ctx, club.ID, club.Treasurer.ID, "", targets)
			if err != nil {
				t.Errorf("runner %d: %v", i, err)
				return
			}
			results[i] = res
		}()
	}
	wg.Wait()
	moved := 0
	for _, r := range results {
		if r != nil {
			moved += r.Reclassified
		}
	}
	if moved != 1 {
		t.Errorf("reclassified %d times, want once", moved)
	}
}
