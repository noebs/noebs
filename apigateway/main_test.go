package gateway

import (
	"fmt"
	"testing"
)

func Test_GenerateApiKey(t *testing.T) {

	t.Run("successful test", func(t *testing.T) {
		if got, err := GenerateAPIKey(); err != nil {
			fmt.Printf("The resultant is: %v, %v", got, err)
		} else {
			fmt.Printf("The resultant is: %v", got)
		}

	})

}
