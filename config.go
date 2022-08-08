package main

import (
	_ "embed"
	"encoding/json"
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
