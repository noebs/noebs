package eventing

import (
	"context"
	"errors"

	"github.com/adonese/noebs/internal/statusevent"
)

type StatusNotificationStore interface {
	StoreStatusNotification(context.Context, statusevent.Event) error
}

type StatusNotificationConsumer struct {
	Reader KafkaMessageReader
	Store  StatusNotificationStore
	Topic  string
}

func (c *StatusNotificationConsumer) ConsumeOnce(ctx context.Context) error {
	if c.Reader == nil || c.Store == nil || c.Topic == "" {
		return errors.New("incomplete status notification consumer")
	}
	message, err := c.Reader.FetchMessage(ctx)
	if err != nil {
		return err
	}
	if message.Topic != c.Topic {
		return ErrUnexpectedKafkaTopic
	}
	event, err := statusevent.Parse(message.Value)
	if err != nil {
		return err
	}
	if err := c.Store.StoreStatusNotification(ctx, event); err != nil {
		return err
	}
	return c.Reader.CommitMessages(ctx, message)
}

func (c *StatusNotificationConsumer) Run(ctx context.Context) error {
	if c == nil || c.Reader == nil || c.Store == nil || c.Topic == "" {
		return errors.New("incomplete status notification consumer")
	}
	defer c.Reader.Close()
	for {
		if err := c.ConsumeOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}
