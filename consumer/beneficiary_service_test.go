package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestGenericBeneficiaryServiceIsTerminalBeforeStore(t *testing.T) {
	service := &Service{}
	ctx := context.Background()
	operations := []func() error{
		func() error {
			_, err := service.ListBeneficiariesForUserID(ctx, "tenant", 42)
			return err
		},
		func() error {
			return service.UpsertBeneficiaryForUserID(ctx, "tenant", 42, ebs_fields.Beneficiary{
				Data: "6011000073184629", BillType: "mobile", Name: "unsafe",
			})
		},
		func() error {
			return service.DeleteBeneficiaryForUserID(ctx, "tenant", 42, "6011000073184629")
		},
	}
	for index, operation := range operations {
		if err := operation(); !errors.Is(err, store.ErrBeneficiaryRetired) {
			t.Fatalf("operation %d error = %v, want %v", index, err, store.ErrBeneficiaryRetired)
		}
	}
}
