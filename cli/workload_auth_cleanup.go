package main

import (
	"context"
	"errors"
	"time"

	"github.com/adonese/noebs/internal/workloadauth"
)

func cleanupExpiredWorkloadNonces(ctx context.Context) error {
	if ctx == nil || database == nil || database.DB == nil {
		return errors.New("workload nonce database is unavailable")
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deleted, err := workloadauth.CleanupExpired(cleanupCtx, database.DB, time.Now().UTC())
	if err != nil {
		return err
	}
	logrusLogger.WithField("deleted", deleted).Print("expired workload nonces removed")
	return nil
}
