package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/jmoiron/sqlx"
)

type transactionRow struct {
	ID        int64        `db:"id"`
	CreatedAt sql.NullTime `db:"created_at"`
	UpdatedAt sql.NullTime `db:"updated_at"`
	Payload   string       `db:"payload"`
}

func (s *Service) ensureDB() (*sqlx.DB, error) {
	if s == nil || s.Store == nil || s.Store.DB == nil || s.Store.DB.DB == nil {
		return nil, errors.New("nil db")
	}
	return s.Store.DB.DB, nil
}

func (s *Service) resolveTenantID(c *fiber.Ctx) string {
	if c != nil {
		if t := c.Get("X-Tenant-ID"); t != "" {
			return t
		}
		if v := c.Locals("tenant_id"); v != nil {
			if t, ok := v.(string); ok && t != "" {
				return t
			}
		}
	}
	if s != nil && s.NoebsConfig.DefaultTenantID != "" {
		return s.NoebsConfig.DefaultTenantID
	}
	return store.DefaultTenantID
}

func decodeTransactionRows(rows []transactionRow) []ebs_fields.EBSResponse {
	out := make([]ebs_fields.EBSResponse, 0, len(rows))
	for _, row := range rows {
		var item ebs_fields.EBSResponse
		if row.Payload != "" {
			_ = json.Unmarshal([]byte(row.Payload), &item)
		}
		item.ID = row.ID
		if row.CreatedAt.Valid {
			item.CreatedAt = row.CreatedAt.Time
		}
		if row.UpdatedAt.Valid {
			item.UpdatedAt = row.UpdatedAt.Time
		}
		out = append(out, item)
	}
	return out
}

func fetchTransactions(ctx context.Context, db *sqlx.DB, query string, args ...any) ([]ebs_fields.EBSResponse, error) {
	rows := []transactionRow{}
	if err := db.SelectContext(ctx, &rows, db.Rebind(query), args...); err != nil {
		return nil, err
	}
	return decodeTransactionRows(rows), nil
}

func normalizeTime(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}
