package accounting

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CounterpartRename is one before/after example from a counterpart re-encode.
type CounterpartRename struct {
	ID     string
	Before string
	After  string
}

// BackfillCounterpartResult reports a counterpart re-encode pass.
type BackfillCounterpartResult struct {
	Scanned int                // rows carrying a legacy substitution symbol
	Changed int                // rows whose decoded value differs
	Samples []CounterpartRename // up to 10 before/after examples for the operator
}

// BackfillCounterpartEncoding re-decodes bank_import_rows.counterpart values
// that still contain the legacy 7-bit substitution symbols (@ | { }) using the
// same case-aware decoder applied on import. It intentionally covers BOOKED
// rows too — counterpart is a display-only field, never referenced by journal
// entries, payments, or post-booking matching — so rewriting it is safe. This
// recovers rows imported before the decoder existed (and booked rows, which a
// re-import never re-parses). Idempotent: a decoded row no longer matches the
// symbol filter. With dryRun the rows are counted and sampled but not written.
// See BRY-222 follow-up (option B).
func BackfillCounterpartEncoding(ctx context.Context, db *pgxpool.Pool, dryRun bool) (BackfillCounterpartResult, error) {
	var res BackfillCounterpartResult

	rows, err := db.Query(ctx,
		`SELECT id, counterpart
		   FROM bank_import_rows
		  WHERE counterpart LIKE '%@%' OR counterpart LIKE '%|%'
		     OR counterpart LIKE '%{%' OR counterpart LIKE '%}%'
		  ORDER BY row_date`)
	if err != nil {
		return res, fmt.Errorf("query counterparts: %w", err)
	}

	type rename struct{ id, after string }
	var pending []rename
	for rows.Next() {
		var id, cp string
		if err := rows.Scan(&id, &cp); err != nil {
			rows.Close()
			return res, fmt.Errorf("scan: %w", err)
		}
		res.Scanned++
		decoded := decodeISO646NOName(cp)
		if decoded == cp {
			continue
		}
		res.Changed++
		pending = append(pending, rename{id: id, after: decoded})
		if len(res.Samples) < 10 {
			res.Samples = append(res.Samples, CounterpartRename{ID: id, Before: cp, After: decoded})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("iterate: %w", err)
	}

	if dryRun {
		return res, nil
	}

	for _, p := range pending {
		if _, err := db.Exec(ctx,
			`UPDATE bank_import_rows SET counterpart = $1 WHERE id = $2`,
			p.after, p.id,
		); err != nil {
			return res, fmt.Errorf("update %s: %w", p.id, err)
		}
	}
	return res, nil
}
