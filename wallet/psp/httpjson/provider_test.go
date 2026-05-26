package httpjson

import (
	"net/http"
	"net/url"
	"testing"
)

func TestAppendQueryForMethodAddsMappedGETFields(t *testing.T) {
	path := appendQueryForMethod(http.MethodGet, "/status", map[string]any{
		"reference": "ref-1",
		"nested": map[string]any{
			"code": "abc",
		},
	})
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatalf("parse path: %v", err)
	}
	if parsed.Path != "/status" {
		t.Fatalf("path = %q, want /status", parsed.Path)
	}
	if got := parsed.Query().Get("reference"); got != "ref-1" {
		t.Fatalf("reference query = %q, want ref-1", got)
	}
	if got := parsed.Query().Get("nested.code"); got != "abc" {
		t.Fatalf("nested.code query = %q, want abc", got)
	}
}

func TestAppendQueryForMethodLeavesPOSTBodyFieldsOutOfURL(t *testing.T) {
	path := appendQueryForMethod(http.MethodPost, "/status", map[string]any{"reference": "ref-1"})
	if path != "/status" {
		t.Fatalf("path = %q, want /status", path)
	}
}
