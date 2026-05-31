package parsing

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func StringParam(values map[string]string, key string) string {
	if values == nil {
		return ""
	}
	return strings.TrimSpace(values[key])
}

func BoolParam(values map[string]string, key string) (bool, error) {
	raw := StringParam(values, key)
	if raw == "" {
		return false, nil
	}
	switch strings.ToLower(raw) {
	case "on", "yes":
		return true, nil
	case "off", "no":
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
	return value, nil
}

func PositiveIntOrDefaultParam(values map[string]string, key string, fallback int) (int, error) {
	value, ok, err := PositiveIntParam(values, key)
	if err != nil {
		return 0, err
	}
	if !ok {
		return fallback, nil
	}
	return value, nil
}

func NonNegativeIntOrDefaultParam(values map[string]string, key string, fallback int) (int, error) {
	value, ok, err := NonNegativeIntParam(values, key)
	if err != nil {
		return 0, err
	}
	if !ok {
		return fallback, nil
	}
	return value, nil
}

func PositiveIntParam(values map[string]string, key string) (int, bool, error) {
	raw := StringParam(values, key)
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, true, fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
	return value, true, nil
}

func NonNegativeIntParam(values map[string]string, key string) (int, bool, error) {
	raw := StringParam(values, key)
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, true, fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
	return value, true, nil
}

func PositiveInt64Param(values map[string]string, key string) (int64, bool, error) {
	raw := StringParam(values, key)
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, true, fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
	return value, true, nil
}

func NonNegativeInt64Param(values map[string]string, key string) (int64, bool, error) {
	raw := StringParam(values, key)
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, true, fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
	return value, true, nil
}

func RFC3339Param(values map[string]string, key string) (time.Time, bool, error) {
	raw := StringParam(values, key)
	if raw == "" {
		return time.Time{}, false, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, true, fmt.Errorf("%w: %s", ErrInvalidField, key)
	}
	return value, true, nil
}
