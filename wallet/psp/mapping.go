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
	Metadata        []string `json:"metadata"`
}

type MappedResponse struct {
	ClientReference string
	TransactionID   string
	Status          string
	Amount          int64
	Currency        string
	Metadata        map[string]any
}

func MapResponse(payload map[string]any, mapping ResponseMapping) MappedResponse {
	return MappedResponse{
		ClientReference: stringFromPaths(payload, mapping.ClientReference),
		TransactionID:   stringFromPaths(payload, mapping.TransactionID),
		Status:          strings.ToLower(stringFromPaths(payload, mapping.Status)),
		Amount:          int64FromPaths(payload, mapping.Amount),
		Currency:        stringFromPaths(payload, mapping.Currency),
		Metadata:        mapFromPaths(payload, mapping.Metadata),
	}
}

func valueFromPaths(payload map[string]any, paths []string) any {
	for _, path := range paths {
		value, ok := valueAtPath(payload, path)
		if ok {
			return value
		}
	}
	return nil
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
	value := valueFromPaths(payload, paths)
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.0f", typed), "0"), ".")
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func int64FromPaths(payload map[string]any, paths []string) int64 {
	value := valueFromPaths(payload, paths)
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func mapFromPaths(payload map[string]any, paths []string) map[string]any {
	value := valueFromPaths(payload, paths)
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}
