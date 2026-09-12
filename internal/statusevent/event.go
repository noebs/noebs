package statusevent

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const Transaction = "transaction.status.changed.v1"
const Verification = "verification.status.changed.v1"

var ErrInvalidEvent = errors.New("invalid status event")

type Event struct {
	ID            uuid.UUID `json:"event_id"`
	Type          string    `json:"type"`
	TenantID      string    `json:"tenant_id"`
	AggregateType string    `json:"aggregate_type"`
	AggregateID   string    `json:"aggregate_id"`
	Version       int64     `json:"version"`
	Status        string    `json:"status"`
	Substatus     string    `json:"substatus"`
	OccurredAt    time.Time `json:"occurred_at"`
	UserID        *int64    `json:"user_id"`
}

func (e Event) Validate() error {
	if e.ID == uuid.Nil || e.TenantID == "" || e.TenantID != strings.TrimSpace(e.TenantID) || e.AggregateID == "" || e.Version < 1 || e.Status == "" || e.Substatus == "" || e.OccurredAt.IsZero() || (e.UserID != nil && *e.UserID < 1) {
		return ErrInvalidEvent
	}
	if (e.Type == Transaction && e.AggregateType == "transaction") || (e.Type == Verification && e.AggregateType == "verification" && e.UserID != nil) {
		return nil
	}
	return ErrInvalidEvent
}

func Parse(payload []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(payload, &e); err != nil {
		return e, err
	}
	return e, e.Validate()
}
