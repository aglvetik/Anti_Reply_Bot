package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"telegram-stop-reply-bot/internal/rules"
)

type SQLiteStore struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply sqlite pragma %q: %w", pragma, err)
		}
	}

	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) LoadActiveRules(ctx context.Context) ([]rules.RuleKey, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT chat_id, protected_user_id, blocked_user_id
FROM block_rules
WHERE enabled = 1
`)
	if err != nil {
		return nil, fmt.Errorf("query active rules: %w", err)
	}
	defer rows.Close()

	activeRules := make([]rules.RuleKey, 0)
	for rows.Next() {
		var key rules.RuleKey
		if err := rows.Scan(&key.ChatID, &key.ProtectedUserID, &key.BlockedUserID); err != nil {
			return nil, fmt.Errorf("scan active rule: %w", err)
		}
		activeRules = append(activeRules, key)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active rules: %w", err)
	}

	return activeRules, nil
}

func (s *SQLiteStore) LoadKnownUsers(ctx context.Context) ([]rules.KnownUser, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT user_id, username, first_name, last_name, is_bot
FROM known_users
`)
	if err != nil {
		return nil, fmt.Errorf("query known users: %w", err)
	}
	defer rows.Close()

	users := make([]rules.KnownUser, 0)
	for rows.Next() {
		var user rules.KnownUser
		var isBot int
		if err := rows.Scan(&user.UserID, &user.Username, &user.FirstName, &user.LastName, &isBot); err != nil {
			return nil, fmt.Errorf("scan known user: %w", err)
		}
		user.IsBot = isBot == 1
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate known users: %w", err)
	}

	return users, nil
}

func (s *SQLiteStore) UpsertRule(ctx context.Context, key rules.RuleKey, enabled bool) error {
	now := time.Now().Unix()
	enabledValue := 0
	if enabled {
		enabledValue = 1
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO block_rules (
    chat_id,
    protected_user_id,
    blocked_user_id,
    enabled,
    created_at,
    updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(chat_id, protected_user_id, blocked_user_id) DO UPDATE SET
    enabled = excluded.enabled,
    updated_at = excluded.updated_at
`, key.ChatID, key.ProtectedUserID, key.BlockedUserID, enabledValue, now, now)
	if err != nil {
		return fmt.Errorf("upsert rule: %w", err)
	}

	return nil
}

func (s *SQLiteStore) UpsertKnownUsers(ctx context.Context, users []rules.KnownUser) error {
	if len(users) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin known users transaction: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO known_users (
    user_id,
    username,
    first_name,
    last_name,
    is_bot,
    updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET
    username = excluded.username,
    first_name = excluded.first_name,
    last_name = excluded.last_name,
    is_bot = excluded.is_bot,
    updated_at = excluded.updated_at
`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare known users upsert: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, user := range users {
		isBot := 0
		if user.IsBot {
			isBot = 1
		}

		if _, err := stmt.ExecContext(ctx, user.UserID, user.Username, user.FirstName, user.LastName, isBot, now); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("upsert known user %d: %w", user.UserID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit known users transaction: %w", err)
	}

	return nil
}
