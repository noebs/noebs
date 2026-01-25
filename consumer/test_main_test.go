package consumer

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if postgresContainer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = postgresContainer.Terminate(ctx)
	}
	os.Exit(code)
}
