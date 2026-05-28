package eventing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/store"
	"github.com/segmentio/kafka-go"
)

var (
	ErrMissingEventStore                 = errors.New("missing transaction event store")
	ErrMissingKafkaBroker                = errors.New("missing kafka broker")
	ErrMissingKafkaWriter                = errors.New("missing kafka writer")
	ErrInvalidEventPublisherBatchSize    = errors.New("invalid event publisher batch size")
	ErrInvalidEventPublisherPollInterval = errors.New("invalid event publisher poll interval")
	ErrUnexpectedKafkaTopic              = errors.New("unexpected kafka topic")
)

type TransactionEventStore interface {
	ClaimPendingTransactionEvents(ctx context.Context, limit int) ([]store.TransactionEvent, error)
	MarkTransactionEventPublished(ctx context.Context, eventID int64) error
	MarkTransactionEventPublishFailed(ctx context.Context, eventID int64, publishErr error) error
}

type KafkaMessageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

type OutboxPublisher struct {
	Store        TransactionEventStore
	Writer       KafkaMessageWriter
	Topic        string
	BatchSize    int
	PollInterval time.Duration
}

func NewKafkaWriter(brokers []string, topic string) (KafkaMessageWriter, error) {
	if err := validateKafkaConnectionConfig(brokers, topic); err != nil {
		return nil, err
	}
	return kafka.NewWriter(kafka.WriterConfig{
		Brokers:      trimNonEmptyStrings(brokers),
		Topic:        strings.TrimSpace(topic),
		Balancer:     &kafka.Hash{},
		BatchSize:    1,
		MaxAttempts:  1,
		RequiredAcks: int(kafka.RequireAll),
		Async:        false,
	}), nil
}

func (p *OutboxPublisher) Validate() error {
	if p == nil {
		return ErrMissingEventStore
	}
	if p.Store == nil {
		return ErrMissingEventStore
	}
	if p.Writer == nil {
		return ErrMissingKafkaWriter
	}
	if strings.TrimSpace(p.Topic) == "" {
		return ErrMissingKafkaTopic
	}
	if p.BatchSize <= 0 {
		return ErrInvalidEventPublisherBatchSize
	}
	if p.PollInterval <= 0 {
		return ErrInvalidEventPublisherPollInterval
	}
	return nil
}

func (p *OutboxPublisher) PublishOnce(ctx context.Context) (int, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	events, err := p.Store.ClaimPendingTransactionEvents(ctx, p.BatchSize)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, event := range events {
		if strings.TrimSpace(event.Topic) != strings.TrimSpace(p.Topic) {
			return published, fmt.Errorf("%w: event %d topic %q", ErrUnexpectedKafkaTopic, event.ID, event.Topic)
		}
		message := kafka.Message{
			Key:   []byte(event.EventKey),
			Value: event.Payload,
			Time:  event.CreatedAt,
		}
		if err := p.Writer.WriteMessages(ctx, message); err != nil {
			markErr := p.Store.MarkTransactionEventPublishFailed(ctx, event.ID, err)
			return published, errors.Join(err, markErr)
		}
		if err := p.Store.MarkTransactionEventPublished(ctx, event.ID); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}

func (p *OutboxPublisher) Run(ctx context.Context) error {
	if err := p.Validate(); err != nil {
		return err
	}
	defer func() {
		_ = p.Writer.Close()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		published, err := p.PublishOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if published > 0 {
			continue
		}
		timer := time.NewTimer(p.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func validateKafkaConnectionConfig(brokers []string, topic string) error {
	if strings.TrimSpace(topic) == "" {
		return ErrMissingKafkaTopic
	}
	for _, broker := range brokers {
		if strings.TrimSpace(broker) != "" {
			return nil
		}
	}
	return ErrMissingKafkaBroker
}

func trimNonEmptyStrings(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}
