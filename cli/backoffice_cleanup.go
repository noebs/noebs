package main

import (
	"context"
	"errors"
	"time"

	"github.com/adonese/noebs/internal/backofficeauth"
)

func cleanupExpiredGatewayAuth(ctx context.Context) error {
	if ctx == nil || database == nil || database.DB == nil {
		return errors.New("gateway authentication database is unavailable")
	}
	repository, err := backofficeauth.NewPostgresStore(database.DB.DB)
	if err != nil {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deleted, err := repository.DeleteExpired(cleanupCtx, time.Now().UTC())
	if err != nil {
		return err
	}
	logrusLogger.WithFields(map[string]any{
		"flows":    deleted.Flows,
		"sessions": deleted.Sessions,
	}).Print("expired gateway authentication records removed")
	return nil
}
