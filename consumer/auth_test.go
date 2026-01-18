package consumer

import (
	"context"
	"os"
	"testing"

	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/option"
)

func TestService_VerifyFirebase(t *testing.T) {
	if os.Getenv("RUN_FIREBASE_TESTS") == "" {
		t.Skip("set RUN_FIREBASE_TESTS=1 to run firebase integration tests")
	}
	_ = newTestEnv(t)
}

func TestService_SendPush(t *testing.T) {
	if os.Getenv("RUN_FIREBASE_TESTS") == "" {
		t.Skip("set RUN_FIREBASE_TESTS=1 to run firebase integration tests")
	}
	opt := option.WithCredentialsFile("../firebase-sdk.json")
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		t.FailNow()
	}
	_ = app
}
