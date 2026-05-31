package consumer

import (
	"fmt"

	"github.com/google/uuid"
)

var newConsumerUUIDString = func() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	return id.String(), nil
}
