package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tcs76321/athanor/migrations"
)

// The single-connection contract, guarded at runtime (F2; ADR-0027 §3).
//
// ADR-0003 caps the pool at one connection, so a transaction — or an
// un-drained *sql.Rows — held across I/O blocks every other goroutine. The
// structural gate (internal/gate/gate_tx_test.go) catches a transaction opened
// in the wrong package; this test catches the other half: a connection taken
// and never returned.
//
// It cannot prove liveness in general. What it does prove is that a
// representative mixed workload completes under a deadline and that the pool
// is empty afterwards. A leak deadlocks the pool, so the test hangs, and the
// race-enabled suite's timeout is the failure signal rather than a quiet pass.

// TestSingleConnectionCapAndPoolDrain pins the ADR-0003 pool size and proves a
// concurrent mixed workload returns the one connection.
func TestSingleConnectionCapAndPoolDrain(t *testing.T) {
	s, _ := openTemp(t)
	if err := Migrate(s.DB(), migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	db := s.DB()

	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1 (ADR-0003; ADR-0027 §1)", got)
	}

	var wg sync.WaitGroup
	// Writers: upserts through the system_state touch trigger, so the
	// workload exercises triggers as well as plain statements.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := db.Exec(
					`INSERT INTO system_state (key, value) VALUES (?, ?)
					 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
					fmt.Sprintf("k-%d-%d", w, i), "v"); err != nil {
					t.Errorf("writer %d: %v", w, err)
					return
				}
			}
		}(w)
	}
	// Readers: open, fully drain, and close a rows iterator while the
	// writers run — the shape that holds the connection if Close is missed.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				rows, err := db.Query(`SELECT COUNT(*) FROM schema_migrations`)
				if err != nil {
					t.Errorf("reader: %v", err)
					return
				}
				for rows.Next() {
					var n int
					if err := rows.Scan(&n); err != nil {
						t.Errorf("reader scan: %v", err)
						break
					}
				}
				if err := rows.Err(); err != nil {
					t.Errorf("reader iterate: %v", err)
				}
				if err := rows.Close(); err != nil {
					t.Errorf("reader close: %v", err)
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("workload did not finish in 30s — a connection was probably leaked (ADR-0027 §3)")
	}

	// Every statement must have released the one connection.
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := db.Stats()
		if st.InUse == 0 && st.OpenConnections <= 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pool not drained: InUse=%d OpenConnections=%d (ADR-0027 §3)",
				st.InUse, st.OpenConnections)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestReadPoolProceedsDuringWrite is ADR-0125's core promise: a UI/SSE read
// does not wait for a job's write transaction. The writer takes BEGIN
// IMMEDIATE; the read pool still sees the pre-transaction snapshot.
func TestReadPoolProceedsDuringWrite(t *testing.T) {
	s, _ := openTemp(t)
	if err := Migrate(s.DB(), migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got := s.ReadDB().Stats().MaxOpenConnections; got < 2 {
		t.Fatalf("read pool MaxOpenConnections = %d, want >= 2", got)
	}
	tx, err := s.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("writer BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO system_state (key, value) VALUES ('held','1')`); err != nil {
		t.Fatalf("writer insert: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var n int
	if err := s.ReadDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("read while write held: %v (the read pool blocked on the writer)", err)
	}
	if n == 0 {
		t.Fatal("read returned no rows")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// TestReadPoolRefusesWrites proves the read handle is actually read-only, so a
// future refactor cannot route a mutation through it silently.
func TestReadPoolRefusesWrites(t *testing.T) {
	s, _ := openTemp(t)
	if err := Migrate(s.DB(), migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := s.ReadDB().Exec(`INSERT INTO system_state (key, value) VALUES ('bad','1')`); err == nil {
		t.Fatal("write through ReadDB succeeded, want a read-only error")
	}
}

// TestConcurrentWritersSerialize exercises the writer pool under contention.
// SQLite serializes writes; the contract is that every statement succeeds
// without SQLITE_BUSY, not that writes run in parallel.
func TestConcurrentWritersSerialize(t *testing.T) {
	s, _ := openTemp(t)
	if err := Migrate(s.DB(), migrations.FS, t.TempDir()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	db := s.DB()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := db.Exec(
					`INSERT INTO system_state (key, value) VALUES (?, ?)
					 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
					fmt.Sprintf("cw-%d-%d", w, i), "v"); err != nil {
					t.Errorf("writer %d: %v", w, err)
					return
				}
			}
		}(w)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent writers did not finish in 30s (possible busy lock)")
	}
}
