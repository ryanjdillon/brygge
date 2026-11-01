package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RowLock holds a row lock from an open transaction, so a test can make the
// code under test stop at that lock for as long as it wants: a booking that
// holds the club's in-process slot while it waits, or an item that is in
// flight when its request is cancelled. It needs a connection of its own, so
// the pool must have one to spare.
type RowLock struct {
	tx  pgx.Tx
	pid int32
	t   *testing.T
	// pool is the pool the blocked-backend poll runs on.
	pool *pgxpool.Pool
}

// HoldRowLock runs lockSQL (a SELECT … FOR UPDATE, say) in a transaction that
// stays open until Release or the end of the test.
func HoldRowLock(t *testing.T, pool *pgxpool.Pool, lockSQL string, args ...any) *RowLock {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("rowlock: begin: %v", err)
	}
	l := &RowLock{tx: tx, t: t, pool: pool}
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&l.pid); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("rowlock: backend pid: %v", err)
	}
	if _, err := tx.Exec(ctx, lockSQL, args...); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("rowlock: lock: %v", err)
	}
	t.Cleanup(l.Release)
	return l
}

// WaitForBlocked waits until another backend is blocked on this lock, so the
// test knows the code under test has reached it. It fails the test when none
// shows up within d.
func (l *RowLock) WaitForBlocked(d time.Duration) {
	l.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		var n int
		// The poll runs on a connection of its own, not on the lock's.
		if err := l.pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, l.pid,
		).Scan(&n); err != nil {
			l.t.Fatalf("rowlock: poll blocked backends: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	l.t.Fatalf("rowlock: no backend blocked on the lock within %s", d)
}

// Release rolls the lock's transaction back; it is safe to call twice.
func (l *RowLock) Release() {
	if l.tx == nil {
		return
	}
	_ = l.tx.Rollback(context.Background())
	l.tx = nil
}
