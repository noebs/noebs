package main

import (
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/eventing"
	walletstore "github.com/adonese/noebs/wallet/store"
)

func newWalletStatusEventPublisher(store *walletstore.Store, cfg ebs_fields.NoebsConfig) (*eventing.OutboxPublisher, error) {
	outbox, err := walletstore.NewStatusEventOutbox(store, cfg.KafkaStatusTopic, time.Minute)
	if err != nil {
		return nil, err
	}
	writer, err := eventing.NewKafkaWriter(cfg.KafkaBrokers, cfg.KafkaStatusTopic)
	if err != nil {
		return nil, err
	}
	publisher := &eventing.OutboxPublisher{
		Store: outbox, Writer: writer, Topic: cfg.KafkaStatusTopic,
		BatchSize: cfg.StatusEventBatchSize, PollInterval: time.Duration(cfg.StatusEventPollIntervalMs) * time.Millisecond,
	}
	if err := publisher.Validate(); err != nil {
		_ = writer.Close()
		return nil, err
	}
	return publisher, nil
}
