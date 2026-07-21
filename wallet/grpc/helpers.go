package walletgrpc

import (
	"database/sql"
	"encoding/json"

	"github.com/adonese/noebs/parsing"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/protobuf/types/known/structpb"
)

func rawFromStruct(input *structpb.Struct) (json.RawMessage, error) {
	if input == nil {
		return nil, nil
	}
	payload := input.AsMap()
	if payload == nil {
		return nil, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func structFromJSON(raw json.RawMessage) (*structpb.Struct, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, nil
	}
	return structpb.NewStruct(payload)
}

func nullStringValue(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func nullInt64Value(value sql.NullInt64) int64 {
	if value.Valid {
		return value.Int64
	}
	return 0
}

func textNullString(value string) sql.NullString {
	text, ok := parsing.Text(value)
	return sql.NullString{String: text, Valid: ok}
}

func textValue(value string) (string, bool) {
	return parsing.Text(value)
}

func missingRequiredText(value string) bool {
	return parsing.MissingText(value)
}

func textOrDefault(value, fallback string) string {
	return parsing.TextOrDefault(value, fallback)
}

func resolveIdempotencyAndReference(idempotencyKey, referenceID string) (string, string, error) {
	if missingRequiredText(idempotencyKey) {
		return "", "", walletstore.ErrMissingIdempotencyKey
	}
	if missingRequiredText(referenceID) {
		return "", "", walletstore.ErrMissingReferenceID
	}
	return idempotencyKey, referenceID, nil
}
