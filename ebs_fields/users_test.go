package ebs_fields

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPaymentToken_UpsertTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("../test.db"), &gorm.Config{})
	if err != nil {
		t.Fatal()
	}

	type args struct {
		transaction EBSResponse
		uuid        string
		db          *gorm.DB
	}
	res := EBSResponse{TranAmount: 100, ResponseMessage: "Successful"}
	tests := []struct {
		name    string
		args    args
		wantErr string
	}{
		{"test_upsert_transaction", args{transaction: res, uuid: "015b88da-1203-4a69-a3ef-e447b6df4ccc", db: db}, "Successful"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PaymentToken{
				Model:       gorm.Model{ID: 6},
				db:          tt.args.db,
				Transaction: tt.args.transaction,
			}
			if err := p.UpsertTransaction(tt.args.transaction); err != nil {
				t.Errorf("PaymentToken.UpsertTransaction() error = %v, wantErr %v", err, tt.wantErr)
			}
			newToken, err := GetTokenWithTransaction(tt.args.uuid, tt.args.db)
			if err != nil {
				t.Errorf("PaymentToken.UpsertTransaction() error = %v, wantErr %v", err, tt.wantErr)
			}
			if newToken.Transaction.TranAmount != tt.args.transaction.TranAmount {
				t.Errorf("PaymentToken.UpsertTransaction() = %v, want %v", newToken.Transaction.TranAmount, tt.args.transaction.TranAmount)
			}

		})
	}
}
