package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func LogAudit(ctx context.Context, db *pgxpool.Pool, clubID, userID, action, entityType, entityID string, oldData, newData any) error {
	var oldJSON, newJSON []byte
	var err error

	if oldData != nil {
		oldJSON, err = json.Marshal(oldData)
		if err != nil {
			return fmt.Errorf("marshaling old data: %w", err)
		}
	}

	if newData != nil {
		newJSON, err = json.Marshal(newData)
		if err != nil {
			return fmt.Errorf("marshaling new data: %w", err)
		}
	}

	_, err = db.Exec(ctx,
		`INSERT INTO audit_log (club_id, user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		clubID, userID, action, entityType, entityID, oldJSON, newJSON,
	)
	if err != nil {
		return fmt.Errorf("inserting audit log: %w", err)
	}

	return nil
}
