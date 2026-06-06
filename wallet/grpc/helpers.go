package walletgrpc

import (
	"database/sql"
	"encoding/json"
	"strings"

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

func missingRequiredText(value string) bool {
	return strings.TrimSpace(value) == ""
}

func textOrDefault(value, fallback string) string {
	if missingRequiredText(value) {
		return fallback
	}
	return value
}

func resolveIdempotencyAndReference(idempotencyKey, referenceID string) (string, string, error) {
	hasIdempotencyKey := !missingRequiredText(idempotencyKey)
	hasReferenceID := !missingRequiredText(referenceID)
	if !hasIdempotencyKey && !hasReferenceID {
		return "", "", walletstore.ErrMissingIdempotencyKey
	}
	if !hasIdempotencyKey {
		idempotencyKey = referenceID
	}
	if !hasReferenceID {
		referenceID = idempotencyKey
	}
	return idempotencyKey, referenceID, nil
}
