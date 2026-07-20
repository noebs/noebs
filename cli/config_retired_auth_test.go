package main

import (
	"errors"
	"testing"
)

func TestRejectRetiredHumanAuthConfig(t *testing.T) {
	for _, key := range retiredHumanAuthConfigKeys {
		t.Run(key, func(t *testing.T) {
			err := rejectRetiredHumanAuthConfig(map[string]interface{}{key: ""})
			if !errors.Is(err, errRetiredHumanAuthConfig) {
				t.Fatalf("error = %v, want %v", err, errRetiredHumanAuthConfig)
			}
		})
	}
}
