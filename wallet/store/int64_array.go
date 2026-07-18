package store

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

type Int64Array []int64

func (a *Int64Array) Scan(src any) error {
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
		return fmt.Errorf("scan int64 array: unsupported type %T", src)
	}
	raw = strings.TrimSpace(raw)
	if raw == "{}" {
		*a = Int64Array{}
		return nil
	}
	if len(raw) < 2 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return fmt.Errorf("scan int64 array: invalid array %q", raw)
	}
	parts := strings.Split(raw[1:len(raw)-1], ",")
	values := make(Int64Array, len(parts))
	for i, part := range parts {
		value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return fmt.Errorf("scan int64 array: invalid value %q", part)
		}
		values[i] = value
	}
	*a = values
	return nil
}

func (a Int64Array) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	values := make([]string, len(a))
	for i, value := range a {
		values[i] = strconv.FormatInt(value, 10)
	}
	return "{" + strings.Join(values, ",") + "}", nil
}
