package main

import (
	"context"
	"errors"
	"time"

	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/transactionauth"
)

func cleanupExpiredGatewayAuth(ctx context.Context) error {
	if ctx == nil || database == nil || database.DB == nil {
		return errors.New("gateway authentication database is unavailable")
	}
	repository, err := backofficeauth.NewPostgresStore(database.DB.DB)
	if err != nil {
		return err
	}
	transactionRepository, err := transactionauth.NewPostgresStore(database.DB.DB)
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	before := time.Now().UTC()
	deleted, err := repository.DeleteExpired(cleanupCtx, before)
	if err != nil {
		return err
	}
	transactionIntents, err := transactionRepository.DeleteExpired(cleanupCtx, before)
	if err != nil {
		return err
	}
	logrusLogger.WithFields(map[string]any{
		"backoffice_flows":    deleted.Flows,
		"backoffice_sessions": deleted.Sessions,
		"transaction_intents": transactionIntents,
	}).Print("expired gateway authentication records removed")
	return nil
}
