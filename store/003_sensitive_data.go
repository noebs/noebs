package store

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(sensitiveDataUp, sensitiveDataDown)
}

func sensitiveDataUp(ctx context.Context, tx *sql.Tx) error {
	driver := migrationDriver
	if driver == "" {
		driver = DriverSQLite
	}

	if err := ensureColumn(ctx, tx, "users", "main_card_enc", "TEXT", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "cards", "pan_enc", "TEXT", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "cards", "ipin_enc", "TEXT", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "cache_cards", "pan_enc", "TEXT", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "tokens", "to_card_enc", "TEXT", driver); err != nil {
		return err
	}

	return nil
}

func sensitiveDataDown(ctx context.Context, tx *sql.Tx) error {
	_ = ctx
	_ = tx
	return nil
}
