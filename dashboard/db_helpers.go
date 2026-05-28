package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
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

func (s *Service) resolveTenantID(c *fiber.Ctx) (string, error) {
	if c == nil {
		return "", store.ErrMissingTenantID
	}

	tenantID, ok := c.Locals("tenant_id").(string)
	if !ok {
		return "", store.ErrMissingTenantID
	}

	return store.ValidateTenantID(tenantID)
}

func (s *Service) requireTenantID(c *fiber.Ctx) (string, bool) {
	tenantID, err := s.resolveTenantID(c)
	if err == nil {
		return tenantID, true
	}
	jsonResponse(c, http.StatusBadRequest, fiber.Map{
		"code":    "invalid_tenant_id",
		"message": err.Error(),
	})
	return "", false
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
