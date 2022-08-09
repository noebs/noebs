package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	firebase "firebase.google.com/go/v4"

	"google.golang.org/api/option"
)

//go:embed .secrets.json
var secretsFile []byte

func parseConfig(data any) error {
	if err := json.Unmarshal(secretsFile, data); err != nil {
		logrusLogger.Printf("Error in parsing config files: %v", err)
		return err
	} else {
		logrusLogger.Printf("the data is: %#v", data)
		return nil
	}
}

func getFirebase() (*firebase.App, error) {
	opt := option.WithCredentialsFile("firebase-sdk.json")
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}
	return app, nil
}
