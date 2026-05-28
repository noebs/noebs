package eventing

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

const TransactionRecordedEventType = "ebs.transaction.recorded.v1"

var (
	ErrMissingKafkaTopic           = errors.New("missing kafka topic")
	ErrInvalidTransactionEventType = errors.New("invalid transaction event type")
)

type TransactionRecordedEvent struct {
	Type        string                 `json:"type"`
	TenantID    string                 `json:"tenant_id"`
	Transaction ebs_fields.EBSResponse `json:"transaction"`
}

func NewTransactionRecordedStoreEvent(topic, tenantID string, transaction ebs_fields.EBSResponse) (store.TransactionEventCreate, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return store.TransactionEventCreate{}, ErrMissingKafkaTopic
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return store.TransactionEventCreate{}, err
	}
	if strings.TrimSpace(transaction.UUID) == "" {
		return store.TransactionEventCreate{}, store.ErrMissingUUID
	}
	transaction.MaskPAN()

	event := TransactionRecordedEvent{
		Type:        TransactionRecordedEventType,
		TenantID:    tenantID,
		Transaction: transaction,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return store.TransactionEventCreate{}, err
	}
	return store.TransactionEventCreate{
		Topic:     topic,
		EventKey:  tenantID + ":" + transaction.UUID,
		EventType: TransactionRecordedEventType,
		Payload:   payload,
	}, nil
}

func ParseTransactionRecordedEvent(payload []byte) (TransactionRecordedEvent, error) {
	if len(payload) == 0 || strings.TrimSpace(string(payload)) == "" {
		return TransactionRecordedEvent{}, store.ErrMissingEventPayload
	}
	var event TransactionRecordedEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return TransactionRecordedEvent{}, err
	}
	if strings.TrimSpace(event.Type) != TransactionRecordedEventType {
		return TransactionRecordedEvent{}, ErrInvalidTransactionEventType
	}
	tenantID, err := store.ValidateTenantID(event.TenantID)
	if err != nil {
		return TransactionRecordedEvent{}, err
	}
	if strings.TrimSpace(event.Transaction.UUID) == "" {
		return TransactionRecordedEvent{}, store.ErrMissingUUID
	}
	event.TenantID = tenantID
	event.Transaction.MaskPAN()
	return event, nil
}
