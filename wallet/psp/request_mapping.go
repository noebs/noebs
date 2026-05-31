package psp

import (
	"fmt"
	"strings"
)

type RequestMapping struct {
	Fields map[string]string `json:"fields"`
	Static map[string]any    `json:"static"`
}

func MapRequest(input map[string]any, mapping RequestMapping) (map[string]any, error) {
	if len(mapping.Fields) == 0 && len(mapping.Static) == 0 {
		return cloneMap(input), nil
	}
	mapped := cloneMap(mapping.Static)
	for targetPath, sourcePath := range mapping.Fields {
		targetPath = strings.TrimSpace(targetPath)
		sourcePath = strings.TrimSpace(sourcePath)
		if targetPath == "" || sourcePath == "" {
			return nil, fmt.Errorf("%w: empty request mapping path", ErrPSPConfigInvalid)
		}
		value, ok := valueAtPath(input, sourcePath)
		if !ok {
			return nil, fmt.Errorf("%w: missing mapped source %q", ErrPSPRequestInvalid, sourcePath)
		}
		if err := setValueAtPath(mapped, targetPath, value); err != nil {
			return nil, err
		}
	}
	return mapped, nil
}

func setValueAtPath(payload map[string]any, path string, value any) error {
	if payload == nil || path == "" {
		return fmt.Errorf("%w: empty request mapping target", ErrPSPConfigInvalid)
	}
	parts := splitPath(path)
	if len(parts) == 0 {
		return fmt.Errorf("%w: empty request mapping target", ErrPSPConfigInvalid)
	}
	current := payload
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
	return nil
}

func splitPath(path string) []string {
	parts := strings.Split(path, ".")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneMap(nested)
			continue
		}
		out[key] = value
	}
	return out
}
