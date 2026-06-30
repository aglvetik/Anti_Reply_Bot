package storage

import (
	"context"
	"database/sql"
	"fmt"
)

const schema = `
CREATE TABLE IF NOT EXISTS block_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL,
    protected_user_id INTEGER NOT NULL,
    blocked_user_id INTEGER NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(chat_id, protected_user_id, blocked_user_id)
);

CREATE INDEX IF NOT EXISTS idx_block_rules_lookup
ON block_rules(chat_id, blocked_user_id, protected_user_id, enabled);

CREATE INDEX IF NOT EXISTS idx_block_rules_active
ON block_rules(enabled);

CREATE TABLE IF NOT EXISTS known_users (
    user_id INTEGER PRIMARY KEY,
    username TEXT,
    first_name TEXT,
    last_name TEXT,
    is_bot INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);
`

func applyMigrations(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}
