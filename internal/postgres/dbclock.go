package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// rowQuerier is satisfied by *pgxpool.Pool and pgx.Tx.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// dbNow returns the DATABASE's wall clock.
//
// Design rule (see ADR-014): the database is the only clock the queue logic may consult. Workers,
// API servers and schedulers run on machines whose clocks drift and jump; if any of them decided
// "is this job due?" or "has this lease expired?" with its own clock, skew would hide jobs, run
// them early, or expire leases wrongly. tests/regression enforces that this package never calls
// time.Now or time.Since.
func dbNow(ctx context.Context, q rowQuerier) (time.Time, error) {
	var t time.Time
	err := q.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&t)
	return t.UTC(), err
}
