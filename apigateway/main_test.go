package gateway

import (
	"context"
	"fmt"
	"testing"

	"github.com/redis/go-redis/v9"
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

func Test_isMember(t *testing.T) {
	type args struct {
		key string
		val string
		r   *redis.Client
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMember(context.Background(), tt.args.key, tt.args.val, tt.args.r); got != tt.want {
				t.Errorf("isMember() = %v, want %v", got, tt.want)
			}
		})
	}
}

type mockRedis struct {
}
