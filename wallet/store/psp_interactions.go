package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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
	IdempotencyKey   sql.NullString `db:"idempotency_key"`
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
	tenantID, err := ValidateTenantID(interaction.TenantID)
	if err != nil {
		return nil, err
	}
	if interaction.PSPProvider == "" {
		return nil, ErrMissingProviderCode
	}
	if interaction.InteractionType == "" {
		return nil, ErrMissingInteractionType
	}
	dispatch := interaction.InteractionType == "deposit_create" || interaction.InteractionType == "payout_send"
	if dispatch && (!interaction.IdempotencyKey.Valid || interaction.IdempotencyKey.String == "") {
		return nil, ErrMissingIdempotencyKey
	}
	if dispatch != interaction.IdempotencyKey.Valid ||
		(interaction.IdempotencyKey.Valid && strings.TrimSpace(interaction.IdempotencyKey.String) != interaction.IdempotencyKey.String) {
		return nil, ErrInvalidIdempotencyKey
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`INSERT INTO psp_interactions(
		tenant_id, psp_provider, psp_transaction_id, client_reference, direction,
		interaction_type, method, url, request_headers, request_body, response_headers,
		response_body, status_code, error_message, idempotency_key
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (tenant_id, psp_provider, interaction_type, idempotency_key)
		WHERE interaction_type IN ('deposit_create', 'payout_send')
		DO NOTHING
	RETURNING *`)
	var stored PSPInteraction
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
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
		interaction.IdempotencyKey,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) && dispatch {
			get := db.Rebind(`SELECT * FROM psp_interactions
				WHERE tenant_id = ? AND psp_provider = ? AND interaction_type = ? AND idempotency_key = ?`)
			if getErr := db.GetContext(ctx, &stored, get,
				tenantID, interaction.PSPProvider, interaction.InteractionType, interaction.IdempotencyKey,
			); getErr == nil {
				return &stored, nil
			} else {
				return nil, getErr
			}
		}
		return nil, err
	}
	return &stored, nil
}
