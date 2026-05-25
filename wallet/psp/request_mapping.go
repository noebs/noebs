package psp

import "strings"

type RequestMapping struct {
	Fields map[string]string `json:"fields"`
	Static map[string]any    `json:"static"`
}

func MapRequest(input map[string]any, mapping RequestMapping) map[string]any {
	if len(mapping.Fields) == 0 && len(mapping.Static) == 0 {
		return cloneMap(input)
	}
	mapped := cloneMap(mapping.Static)
	for targetPath, sourcePath := range mapping.Fields {
		value, ok := valueAtPath(input, sourcePath)
		if !ok {
			continue
		}
		setValueAtPath(mapped, targetPath, value)
	}
	return mapped
}

func setValueAtPath(payload map[string]any, path string, value any) {
	if payload == nil || path == "" {
		return
	}
	parts := splitPath(path)
	if len(parts) == 0 {
		return
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
