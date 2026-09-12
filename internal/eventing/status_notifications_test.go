package eventing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/statusevent"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type statusSink struct {
	err   error
	calls int
}

func (s *statusSink) StoreStatusNotification(context.Context, statusevent.Event) error {
	s.calls++
	return s.err
}

func TestStatusConsumerCommitsOnlyAfterDurableNotification(t *testing.T) {
	uid := int64(7)
	event := statusevent.Event{ID: uuid.New(), Type: statusevent.Verification, TenantID: "tenant", AggregateType: "verification", AggregateID: uuid.NewString(), Version: 2, Status: "pending", Substatus: "manual_review", OccurredAt: time.Now(), UserID: &uid}
	payload, _ := json.Marshal(event)
	reader := &fakeKafkaReader{message: kafka.Message{Topic: "status", Value: payload}}
	sink := &statusSink{err: errors.New("database unavailable")}
	consumer := &StatusNotificationConsumer{Reader: reader, Store: sink, Topic: "status"}
	if err := consumer.ConsumeOnce(t.Context()); err == nil || reader.committed {
		t.Fatal("acknowledged event without storing notification")
	}
	sink.err = nil
	if err := consumer.ConsumeOnce(t.Context()); err != nil || !reader.committed || sink.calls != 2 {
		t.Fatalf("retry error=%v committed=%v calls=%d", err, reader.committed, sink.calls)
	}
}

func TestInvalidStatusEventNeverAcknowledged(t *testing.T) {
	reader := &fakeKafkaReader{message: kafka.Message{Topic: "status", Value: []byte(`{"type":"unknown"}`)}}
	sink := &statusSink{}
	consumer := &StatusNotificationConsumer{Reader: reader, Store: sink, Topic: "status"}
	if err := consumer.ConsumeOnce(t.Context()); err == nil || reader.committed || sink.calls != 0 {
		t.Fatal("invalid event consumed")
	}
}
