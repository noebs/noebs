package store

import (
	"database/sql/driver"
	"fmt"
	"strings"
)

type StringArray []string

func (a *StringArray) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	var raw string
	switch value := src.(type) {
	case string:
		raw = value
	case []byte:
		raw = string(value)
	default:
		return fmt.Errorf("scan string array: unsupported type %T", src)
	}
	values, err := parsePostgresTextArray(raw)
	if err != nil {
		return err
	}
	*a = values
	return nil
}

func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	parts := make([]string, 0, len(a))
	for _, value := range a {
		escaped := strings.ReplaceAll(value, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		parts = append(parts, `"`+escaped+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func parsePostgresTextArray(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "{}" {
		return []string{}, nil
	}
	if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return nil, fmt.Errorf("scan string array: invalid array %q", raw)
	}
	body := raw[1 : len(raw)-1]
	values := []string{}
	var current strings.Builder
	inQuotes := false
	escaped := false
	for _, r := range body {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			values = append(values, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if inQuotes || escaped {
		return nil, fmt.Errorf("scan string array: invalid array %q", raw)
	}
	values = append(values, current.String())
	return values, nil
}
