package eventing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/adminreporting"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/segmentio/kafka-go"
)

func TestTransactionRecordedEventRequiresExplicitInputsAndMasksPAN(t *testing.T) {
	_, err := NewTransactionRecordedStoreEvent("", "tenant-a", ebs_fields.EBSResponse{UUID: "tx-1"})
	if !errors.Is(err, ErrMissingKafkaTopic) {
		t.Fatalf("missing topic error = %v, want %v", err, ErrMissingKafkaTopic)
	}
	_, err = NewTransactionRecordedStoreEvent("topic", "", ebs_fields.EBSResponse{UUID: "tx-1"})
	if !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("missing tenant error = %v, want %v", err, store.ErrMissingTenantID)
	}
	_, err = NewTransactionRecordedStoreEvent("topic", "tenant-a", ebs_fields.EBSResponse{})
	if !errors.Is(err, store.ErrMissingUUID) {
		t.Fatalf("missing uuid error = %v, want %v", err, store.ErrMissingUUID)
	}

	outboxEvent, err := NewTransactionRecordedStoreEvent("topic", "tenant-a", ebs_fields.EBSResponse{
		UUID: "tx-1",
		PAN:  "9222081700009999",
	})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	if outboxEvent.Topic != "topic" || outboxEvent.EventKey != "tenant-a:tx-1" || outboxEvent.EventType != TransactionRecordedEventType {
		t.Fatalf("outbox event = %+v", outboxEvent)
	}
	parsed, err := ParseTransactionRecordedEvent(outboxEvent.Payload)
	if err != nil {
		t.Fatalf("parse event: %v", err)
	}
	if parsed.Transaction.PAN != "922208*****9999" {
		t.Fatalf("masked PAN = %q", parsed.Transaction.PAN)
	}
}

func TestOutboxPublisherPublishesAndMarksEvents(t *testing.T) {
	storeSvc := &fakeTransactionEventStore{
		events: []store.TransactionEvent{{
			ID:        7,
			TenantID:  "tenant-a",
			Topic:     "topic",
			EventKey:  "tenant-a:tx-1",
			EventType: TransactionRecordedEventType,
			Payload:   []byte(`{"type":"ebs.transaction.recorded.v1"}`),
			CreatedAt: time.Unix(10, 0).UTC(),
		}},
	}
	writer := &fakeKafkaWriter{}
	publisher := &OutboxPublisher{
		Store:        storeSvc,
		Writer:       writer,
		Topic:        "topic",
		BatchSize:    10,
		PollInterval: time.Millisecond,
	}
	published, err := publisher.PublishOnce(context.Background())
	if err != nil {
		t.Fatalf("publish once: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1", published)
	}
	if len(writer.messages) != 1 || string(writer.messages[0].Key) != "tenant-a:tx-1" {
		t.Fatalf("written messages = %+v", writer.messages)
	}
	if len(storeSvc.publishedIDs) != 1 || storeSvc.publishedIDs[0] != 7 {
		t.Fatalf("published IDs = %v", storeSvc.publishedIDs)
	}
}

func TestOutboxPublisherMarksFailedWrite(t *testing.T) {
	writeErr := errors.New("write failed")
	storeSvc := &fakeTransactionEventStore{
		events: []store.TransactionEvent{{ID: 8, Topic: "topic", EventKey: "key", Payload: []byte(`{}`)}},
	}
	publisher := &OutboxPublisher{
		Store:        storeSvc,
		Writer:       &fakeKafkaWriter{writeErr: writeErr},
		Topic:        "topic",
		BatchSize:    1,
		PollInterval: time.Millisecond,
	}
	_, err := publisher.PublishOnce(context.Background())
	if !errors.Is(err, writeErr) {
		t.Fatalf("publish error = %v, want %v", err, writeErr)
	}
	if len(storeSvc.failedIDs) != 1 || storeSvc.failedIDs[0] != 8 {
		t.Fatalf("failed IDs = %v", storeSvc.failedIDs)
	}
}

func TestAdminReportingProjectorStoresProjectionThenCommits(t *testing.T) {
	outboxEvent, err := NewTransactionRecordedStoreEvent("topic", "tenant-a", ebs_fields.EBSResponse{UUID: "tx-1", TerminalID: "terminal-a"})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	reader := &fakeKafkaReader{message: kafka.Message{Topic: "topic", Key: []byte(outboxEvent.EventKey), Value: outboxEvent.Payload}}
	service := &fakeProjectionService{}
	projector := &AdminReportingProjector{Reader: reader, Service: service, Topic: "topic"}
	if err := projector.ProjectOnce(context.Background()); err != nil {
		t.Fatalf("project once: %v", err)
	}
	if service.tenantID != "tenant-a" || service.transaction == nil || service.transaction.UUID != "tx-1" {
		t.Fatalf("projection service got tenant=%q tx=%+v", service.tenantID, service.transaction)
	}
	if !reader.committed {
		t.Fatalf("message was not committed")
	}
}

type fakeTransactionEventStore struct {
	events       []store.TransactionEvent
	publishedIDs []int64
	failedIDs    []int64
}

func (s *fakeTransactionEventStore) ClaimPendingTransactionEvents(context.Context, int) ([]store.TransactionEvent, error) {
	return s.events, nil
}

func (s *fakeTransactionEventStore) MarkTransactionEventPublished(_ context.Context, eventID int64) error {
	s.publishedIDs = append(s.publishedIDs, eventID)
	return nil
}

func (s *fakeTransactionEventStore) MarkTransactionEventPublishFailed(_ context.Context, eventID int64, _ error) error {
	s.failedIDs = append(s.failedIDs, eventID)
	return nil
}

type fakeKafkaWriter struct {
	messages []kafka.Message
	writeErr error
}

func (w *fakeKafkaWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	if w.writeErr != nil {
		return w.writeErr
	}
	w.messages = append(w.messages, msgs...)
	return nil
}

func (w *fakeKafkaWriter) Close() error {
	return nil
}

type fakeKafkaReader struct {
	message   kafka.Message
	committed bool
}

func (r *fakeKafkaReader) FetchMessage(context.Context) (kafka.Message, error) {
	return r.message, nil
}

func (r *fakeKafkaReader) CommitMessages(context.Context, ...kafka.Message) error {
	r.committed = true
	return nil
}

func (r *fakeKafkaReader) Close() error {
	return nil
}

type fakeProjectionService struct {
	tenantID    string
	transaction *ebs_fields.EBSResponse
}

func (s *fakeProjectionService) StoreTransactionProjection(_ context.Context, tenantID string, cmd adminreporting.TransactionProjectionCommand) error {
	s.tenantID = tenantID
	s.transaction = cmd.Transaction
	return nil
}
