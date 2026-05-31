package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func forceConsumerUUIDError(t *testing.T, err error) {
	t.Helper()
	original := newConsumerUUIDString
	newConsumerUUIDString = func() (string, error) {
		return "", err
	}
	t.Cleanup(func() {
		newConsumerUUIDString = original
	})
}

func TestIPINFlowsPropagateUUIDGenerationErrors(t *testing.T) {
	uuidErr := errors.New("uuid source unavailable")
	for _, tc := range []struct {
		name string
		run  func(*Service) error
	}{
		{
			name: "GenerateIpin",
			run: func(service *Service) error {
				_, err := service.GenerateIpin(context.Background(), "tenant-a", ebs_fields.ConsumerGenerateIPin{})
				return err
			},
		},
		{
			name: "CompleteIpin",
			run: func(service *Service) error {
				_, err := service.CompleteIpin(context.Background(), "tenant-a", ebs_fields.ConsumerGenerateIPinCompletion{
					Ipin: "123456",
					Otp:  "123456",
				})
				return err
			},
		},
		{
			name: "IPINKey",
			run: func(service *Service) error {
				_, err := service.IPINKey(context.Background(), "tenant-a", ebs_fields.ConsumerGenerateIPINFields{})
				return err
			},
		},
		{
			name: "GetIpinPubKey",
			run: func(service *Service) error {
				return service.GetIpinPubKey(context.Background(), "tenant-a")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forceConsumerUUIDError(t, uuidErr)
			service := &Service{Store: &store.Store{}}

			err := tc.run(service)
			if !errors.Is(err, uuidErr) {
				t.Fatalf("error = %v, want %v", err, uuidErr)
			}
		})
	}
}
