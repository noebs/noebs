package psp

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type ResponseMapping struct {
	ClientReference []string `json:"client_reference"`
	TransactionID   []string `json:"transaction_id"`
	Status          []string `json:"status"`
	Amount          []string `json:"amount"`
	Currency        []string `json:"currency"`
	Direction       []string `json:"direction"`
	Message         []string `json:"message"`
	Metadata        []string `json:"metadata"`
}

type MappedResponse struct {
	ClientReference string
	TransactionID   string
	Status          string
	Amount          int64
	Currency        string
	Direction       string
	Message         string
	Metadata        map[string]any
}

func MapResponse(payload map[string]any, mapping ResponseMapping) (MappedResponse, error) {
	amount, err := int64FromPaths(payload, mapping.Amount)
	if err != nil {
		return MappedResponse{}, err
	}
	return MappedResponse{
		ClientReference: stringFromPaths(payload, mapping.ClientReference),
		TransactionID:   stringFromPaths(payload, mapping.TransactionID),
		Status:          strings.ToLower(stringFromPaths(payload, mapping.Status)),
		Amount:          amount,
		Currency:        stringFromPaths(payload, mapping.Currency),
		Direction:       stringFromPaths(payload, mapping.Direction),
		Message:         stringFromPaths(payload, mapping.Message),
		Metadata:        mapFromPaths(payload, mapping.Metadata),
	}, nil
}

func valueFromPaths(payload map[string]any, paths []string) (string, any, bool) {
	for _, path := range paths {
		value, ok := valueAtPath(payload, path)
		if ok {
			return strings.TrimSpace(path), value, true
		}
	}
	return "", nil, false
}

func valueAtPath(payload map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	var current any = payload
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = next
		default:
			return nil, false
		}
	}
	return current, true
}

func stringFromPaths(payload map[string]any, paths []string) string {
	_, value, ok := valueFromPaths(payload, paths)
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func int64FromPaths(payload map[string]any, paths []string) (int64, error) {
	if len(paths) == 0 {
		return 0, nil
	}
	path, value, ok := valueFromPaths(payload, paths)
	if !ok {
		return 0, fmt.Errorf("%w: missing amount at configured paths", ErrPSPResponseInvalid)
	}
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int16:
		return int64(typed), nil
	case int8:
		return int64(typed), nil
	case uint:
		if uint64(typed) > uint64(^uint64(0)>>1) {
			return 0, invalidMappedField("amount", path, value)
		}
		return int64(typed), nil
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0, invalidMappedField("amount", path, value)
		}
		return int64(typed), nil
	case uint32:
		return int64(typed), nil
	case uint16:
		return int64(typed), nil
	case uint8:
		return int64(typed), nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, invalidMappedField("amount", path, value)
		}
		return parsed, nil
	case string:
		if typed == "" || strings.TrimSpace(typed) != typed {
			return 0, invalidMappedField("amount", path, value)
		}
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return 0, invalidMappedField("amount", path, value)
		}
		return parsed, nil
	default:
		return 0, invalidMappedField("amount", path, value)
	}
}

func mapFromPaths(payload map[string]any, paths []string) map[string]any {
	_, value, ok := valueFromPaths(payload, paths)
	if !ok {
		return nil
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func invalidMappedField(field, path string, value any) error {
	return fmt.Errorf("%w: invalid %s at %q (%T)", ErrPSPResponseInvalid, field, path, value)
}
