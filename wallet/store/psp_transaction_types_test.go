package store

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRawJSONHandlesSQLNull(t *testing.T) {
	var raw RawJSON
	if err := raw.Scan(nil); err != nil {
		t.Fatalf("scan null: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("expected empty raw json after null scan, got %q", string(raw))
	}

	value, err := raw.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	if value != nil {
		t.Fatalf("expected nil driver value, got %v", value)
	}
}

func TestRawJSONPreservesJSONEncoding(t *testing.T) {
	raw := RawJSON(`{"status":"ok"}`)
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"status":"ok"}` {
		t.Fatalf("expected raw JSON object, got %s", data)
	}

	var decoded RawJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	value, err := decoded.Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	rawValue, ok := value.([]byte)
	if !ok {
		t.Fatalf("expected []byte driver value, got %T", value)
	}
	if !bytes.Equal(rawValue, []byte(`{"status":"ok"}`)) {
		t.Fatalf("expected raw JSON driver value, got %s", rawValue)
	}
}
