package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(backfillUp, backfillDown)
}

func backfillUp(ctx context.Context, tx *sql.Tx) error {
	driver := migrationDriver
	if driver == "" {
		driver = DriverSQLite
	}
	tenantID := migrationDefaultTenant
	if tenantID == "" {
		tenantID = DefaultTenantID
	}

	tablesWithTenant := []string{
		"users",
		"auth_accounts",
		"cards",
		"cache_cards",
		"cache_billers",
		"beneficiaries",
		"tokens",
		"transactions",
		"push_data",
		"api_keys",
		"login_metrics",
		"meter_names",
		"kyc",
		"passports",
		"merchant_issues",
	}

	tenantColDef := fmt.Sprintf("TEXT NOT NULL DEFAULT '%s'", sqlLiteral(tenantID))
	for _, table := range tablesWithTenant {
		if err := ensureColumn(ctx, tx, table, "tenant_id", tenantColDef, driver); err != nil {
			return err
		}
		if err := backfillTenantID(ctx, tx, table, tenantID, driver); err != nil {
			return err
		}
	}

	if err := ensureColumn(ctx, tx, "transactions", "terminal_id", "TEXT", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "transactions", "system_trace_audit_number", "INTEGER", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "transactions", "approval_code", "TEXT", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "transactions", "tran_fee", "NUMERIC", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "transactions", "sender_pan", "TEXT", driver); err != nil {
		return err
	}
	if err := ensureColumn(ctx, tx, "transactions", "receiver_pan", "TEXT", driver); err != nil {
		return err
	}

	if err := ensureColumn(ctx, tx, "push_data", "to_device", "TEXT", driver); err != nil {
		return err
	}
	if exists, err := columnExists(ctx, tx, "push_data", "to", driver); err != nil {
		return err
	} else if exists {
		if err := copyPushDataToDevice(ctx, tx); err != nil {
			return err
		}
	}

	if err := ensureDefaultTenant(ctx, tx, tenantID, driver); err != nil {
		return err
	}

	return nil
}

func backfillDown(ctx context.Context, tx *sql.Tx) error {
	_ = ctx
	_ = tx
	return nil
}

func ensureDefaultTenant(ctx context.Context, tx *sql.Tx, tenantID, driver string) error {
	exists, err := tableExists(ctx, tx, "tenants", driver)
	if err != nil || !exists {
		return err
	}
	now := time.Now().UTC()
	switch driver {
	case DriverPostgres:
		_, err = tx.ExecContext(ctx, `INSERT INTO tenants (id, name, created_at) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`, tenantID, tenantID, now)
	default:
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO tenants (id, name, created_at) VALUES (?, ?, ?)`, tenantID, tenantID, now)
	}
	return err
}

func ensureColumn(ctx context.Context, tx *sql.Tx, table, column, columnDef, driver string) error {
	ok, err := tableExists(ctx, tx, table, driver)
	if err != nil || !ok {
		return err
	}
	exists, err := columnExists(ctx, tx, table, column, driver)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, columnDef)
	_, err = tx.ExecContext(ctx, stmt)
	return err
}

func backfillTenantID(ctx context.Context, tx *sql.Tx, table, tenantID, driver string) error {
	ok, err := tableExists(ctx, tx, table, driver)
	if err != nil || !ok {
		return err
	}
	exists, err := columnExists(ctx, tx, table, "tenant_id", driver)
	if err != nil || !exists {
		return err
	}
	switch driver {
	case DriverPostgres:
		_, err = tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET tenant_id = $1 WHERE tenant_id IS NULL OR tenant_id = ''", table), tenantID)
	default:
		_, err = tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET tenant_id = ? WHERE tenant_id IS NULL OR tenant_id = ''", table), tenantID)
	}
	return err
}

func copyPushDataToDevice(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE push_data SET to_device = "to" WHERE (to_device IS NULL OR to_device = '') AND "to" IS NOT NULL`)
	return err
}

func tableExists(ctx context.Context, tx *sql.Tx, table, driver string) (bool, error) {
	switch driver {
	case DriverPostgres:
		var exists bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1
		)`, table).Scan(&exists)
		return exists, err
	default:
		var name string
		err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
		if err == sql.ErrNoRows {
			return false, nil
		}
		return err == nil, err
	}
}

func columnExists(ctx context.Context, tx *sql.Tx, table, column, driver string) (bool, error) {
	switch driver {
	case DriverPostgres:
		var exists bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
		return exists, err
	default:
		rows, err := tx.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name string
			var ctype string
			var notnull int
			var dflt sql.NullString
			var pk int
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				return false, err
			}
			if name == column {
				return true, nil
			}
		}
		return false, rows.Err()
	}
}

func sqlLiteral(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
