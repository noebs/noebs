package interop

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// ProtocolHeaders accepts the pinned SDK's numeric Content-Length while keeping
// participant headers single-valued and case-insensitive. Raw envelopes remain
// unchanged in SQL. A duplicate header, including a differently cased spelling,
// is rejected instead of letting JSON map order decide protocol authority.
type ProtocolHeaders map[string]string

func (headers *ProtocolHeaders) UnmarshalJSON(raw []byte) error {
	// Optional envelopes may have no headers. Callers still require source and
	// destination explicitly wherever the envelope establishes authority.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*headers = nil
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrProtocol
	}
	result := ProtocolHeaders{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return ErrProtocol
		}
		name, ok := token.(string)
		if !ok || !httpguts.ValidHeaderFieldName(name) {
			return ErrProtocol
		}
		name = strings.ToLower(name)
		if _, exists := result[name]; exists {
			return ErrProtocol
		}
		var value any
		if decoder.Decode(&value) != nil {
			return ErrProtocol
		}
		switch typed := value.(type) {
		case string:
			if !httpguts.ValidHeaderFieldValue(typed) {
				return ErrProtocol
			}
			result[name] = typed
		case json.Number:
			if name != "content-length" {
				return ErrProtocol
			}
			if _, err = strconv.ParseUint(string(typed), 10, 64); err != nil {
				return ErrProtocol
			}
			result[name] = string(typed)
		default:
			return ErrProtocol
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return ErrProtocol
	}
	if _, err = decoder.Token(); err != io.EOF {
		return ErrProtocol
	}
	*headers = result
	return nil
}
