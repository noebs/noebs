package parsing

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	ErrMissingField = errors.New("missing field")
	ErrInvalidField = errors.New("invalid field")
)

func String(fields map[string]any, key string) (string, bool) {
	if fields == nil {
		return "", false
	}
	value, ok := fields[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	return text, true
}

func StringOrDefault(fields map[string]any, key, fallback string) (string, bool) {
	text, ok := String(fields, key)
	if !ok {
		return fallback, false
	}
	return text, true
}

func Text(value string) (string, bool) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", false
	}
	return text, true
}

func MissingText(value string) bool {
	_, ok := Text(value)
	return !ok
}

func TextOrDefault(value, fallback string) string {
	text, ok := Text(value)
	if !ok {
		return fallback
	}
	return text
}

func RequiredString(fields map[string]any, key string) (string, error) {
	if fields == nil {
		return "", fmt.Errorf("%w: %s", ErrMissingField, key)
	}
	value, ok := fields[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrMissingField, key)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
	return text, nil
}

func RequiredFloat64(fields map[string]any, key string) (float64, error) {
	if fields == nil {
		return 0, fmt.Errorf("%w: %s", ErrMissingField, key)
	}
	value, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrMissingField, key)
	}
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %s", ErrInvalidField, key)
		}
		return requireFiniteFloat64(parsed, key)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("%w: %s", ErrInvalidField, key)
		}
		return requireFiniteFloat64(parsed, key)
	case float64:
		return requireFiniteFloat64(typed, key)
	case float32:
		return requireFiniteFloat64(float64(typed), key)
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case int32:
		return float64(typed), nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
}

func requireFiniteFloat64(value float64, key string) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
	return value, nil
}
