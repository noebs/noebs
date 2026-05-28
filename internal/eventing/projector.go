package eventing

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/adminreporting"
	"github.com/segmentio/kafka-go"
)

var (
	ErrMissingKafkaConsumerGroup = errors.New("missing kafka consumer group")
	ErrMissingKafkaReader        = errors.New("missing kafka reader")
	ErrMissingProjectionService  = errors.New("missing projection service")
)

type KafkaMessageReader interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

type TransactionProjectionService interface {
	StoreTransactionProjection(ctx context.Context, tenantID string, cmd adminreporting.TransactionProjectionCommand) error
}

type AdminReportingProjector struct {
	Reader  KafkaMessageReader
	Service TransactionProjectionService
	Topic   string
}

func NewKafkaReader(brokers []string, topic, groupID string) (KafkaMessageReader, error) {
	if err := validateKafkaConnectionConfig(brokers, topic); err != nil {
		return nil, err
	}
	if strings.TrimSpace(groupID) == "" {
		return nil, ErrMissingKafkaConsumerGroup
	}
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:        trimNonEmptyStrings(brokers),
		Topic:          strings.TrimSpace(topic),
		GroupID:        strings.TrimSpace(groupID),
		MinBytes:       1,
		MaxBytes:       10 * 1024 * 1024,
		CommitInterval: 0,
	}), nil
}

func (p *AdminReportingProjector) Validate() error {
	if p == nil {
		return ErrMissingProjectionService
	}
	if p.Reader == nil {
		return ErrMissingKafkaReader
	}
	if p.Service == nil {
		return ErrMissingProjectionService
	}
	if strings.TrimSpace(p.Topic) == "" {
		return ErrMissingKafkaTopic
	}
	return nil
}

func (p *AdminReportingProjector) ProjectOnce(ctx context.Context) error {
	if err := p.Validate(); err != nil {
		return err
	}
	message, err := p.Reader.FetchMessage(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(message.Topic) != "" && strings.TrimSpace(message.Topic) != strings.TrimSpace(p.Topic) {
		return ErrUnexpectedKafkaTopic
	}
	event, err := ParseTransactionRecordedEvent(message.Value)
	if err != nil {
		return err
	}
	if err := p.Service.StoreTransactionProjection(ctx, event.TenantID, adminreporting.TransactionProjectionCommand{Transaction: &event.Transaction}); err != nil {
		return err
	}
	return p.Reader.CommitMessages(ctx, message)
}

func (p *AdminReportingProjector) Run(ctx context.Context) error {
	if err := p.Validate(); err != nil {
		return err
	}
	defer func() {
		_ = p.Reader.Close()
	}()
	for {
		if err := p.ProjectOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}
