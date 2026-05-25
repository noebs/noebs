package store

import (
	"context"
	"database/sql"
	"time"
)

type PSPInteraction struct {
	ID               int64          `db:"id"`
	TenantID         string         `db:"tenant_id"`
	PSPProvider      string         `db:"psp_provider"`
	PSPTransactionID sql.NullString `db:"psp_transaction_id"`
	ClientReference  sql.NullString `db:"client_reference"`
	Direction        sql.NullString `db:"direction"`
	InteractionType  string         `db:"interaction_type"`
	Method           sql.NullString `db:"method"`
	URL              sql.NullString `db:"url"`
	RequestHeaders   RawJSON        `db:"request_headers"`
	RequestBody      RawJSON        `db:"request_body"`
	ResponseHeaders  RawJSON        `db:"response_headers"`
	ResponseBody     RawJSON        `db:"response_body"`
	StatusCode       sql.NullInt64  `db:"status_code"`
	ErrorMessage     sql.NullString `db:"error_message"`
	CreatedAt        time.Time      `db:"created_at"`
}

func (s *Store) RecordPSPInteraction(ctx context.Context, interaction PSPInteraction) (*PSPInteraction, error) {
	if interaction.TenantID == "" {
		return nil, ErrMissingTenantID
	}
	if interaction.PSPProvider == "" {
		return nil, ErrMissingProviderCode
	}
	if interaction.InteractionType == "" {
		return nil, ErrMissingInteractionType
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`INSERT INTO psp_interactions(
		tenant_id, psp_provider, psp_transaction_id, client_reference, direction,
		interaction_type, method, url, request_headers, request_body, response_headers,
		response_body, status_code, error_message
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored PSPInteraction
	if err := db.GetContext(ctx, &stored, stmt,
		interaction.TenantID,
		interaction.PSPProvider,
		interaction.PSPTransactionID,
		interaction.ClientReference,
		interaction.Direction,
		interaction.InteractionType,
		interaction.Method,
		interaction.URL,
		interaction.RequestHeaders,
		interaction.RequestBody,
		interaction.ResponseHeaders,
		interaction.ResponseBody,
		interaction.StatusCode,
		interaction.ErrorMessage,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}
