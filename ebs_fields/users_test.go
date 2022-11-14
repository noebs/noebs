package ebs_fields

import (
	"reflect"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var testDB, err = gorm.Open(sqlite.Open("../test.db"), &gorm.Config{})

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
				db:          tt.args.db,
				Transaction: tt.args.transaction,
			}
			if err := p.UpsertTransaction(tt.args.transaction, tt.args.uuid); err != nil {
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

func TestGetTokenByUUID(t *testing.T) {
	type args struct {
		uuid   string
		mobile string
		db     *gorm.DB
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{"Test_to_card", args{"015b88da-1203-4a69-a3ef-e447b6df4ccc", "0912141665", testDB}, "working", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetTokenByUUID(tt.args.uuid, tt.args.db)
			if err != nil {
				t.Errorf("the error is: %v", err)
			}
			// test that the card is:
			if got.User.Cards[0].Name != tt.want {
				t.Errorf("the error is: %v", err)
			}

		})
	}
}

func TestCard_UpdateCard(t *testing.T) {

	type args struct {
		card   Card
		db     *gorm.DB
		expiry string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"update card", args{card: Card{CardIdx: "1111", Expiry: "1234", UserID: 1}, db: testDB, expiry: "6969"}, "1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := UpdateCard(tt.args.card, tt.args.db); err != nil {
				t.Errorf("Card.UpdateCard() error = %v", err)
			}
			if tt.args.card.Expiry != tt.want {
				t.Errorf("Old card is not updated: old: %v, new: %v", tt.args.expiry, tt.want)
			}
		})
	}
}

func TestDeleteCard(t *testing.T) {
	type args struct {
		card   Card
		db     *gorm.DB
		expiry string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"delete card", args{card: Card{CardIdx: "1111", Expiry: "1234", UserID: 1}, db: testDB, expiry: "6969"}, "1234"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := DeleteCard(tt.args.card, tt.args.db); err != nil {
				t.Errorf("DeleteCard() error = %v", err)
			}
		})
	}
}

func TestGetTokenWithResult(t *testing.T) {
	type args struct {
		uuid string
		db   *gorm.DB
	}
	tests := []struct {
		name    string
		args    args
		want    PaymentToken
		wantErr bool
	}{
		{"Test_to_card", args{"015b88da-1203-4a69-a3ef-e447b6df4ccc", testDB}, PaymentToken{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetTokenWithResult(tt.args.uuid, tt.args.db)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetTokenWithResult() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetTokenWithResult() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUser_UpsertBeneficiary(t *testing.T) {

	type args struct {
		beneficiary []Beneficiary
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"test upserting a card", args{beneficiary: []Beneficiary{{Data: "0912141679", BillType: "001001001"}, {Data: "012114141", BillType: "001001001"}, {Data: "012112142", BillType: "001001001"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := User{}
			u.db = testDB
			u.ID = 1
			if err := u.UpsertBeneficiary(tt.args.beneficiary); (err != nil) != tt.wantErr {
				t.Errorf("User.UpsertBeneficiary() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeleteBeneficiary(t *testing.T) {
	type args struct {
		beneficiary Beneficiary
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"test deleting a beneficiary", args{beneficiary: Beneficiary{Data: "0912141679", BillType: "001001001", UserID: 1}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := DeleteBeneficiary(tt.args.beneficiary, testDB); (err != nil) != tt.wantErr {
				t.Errorf("User.UpsertBeneficiary() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewUserWithBeneficiaries(t *testing.T) {
	type args struct {
		mobile string
		db     *gorm.DB
	}
	tests := []struct {
		name    string
		args    args
		want    *User
		wantErr bool
	}{
		{"test getting beneficiaries", args{mobile: "0111493885", db: testDB}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewUserWithBeneficiaries(tt.args.mobile, tt.args.db)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewUserWithBeneficiaries() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			var match bool
			log.Printf("the beneficiaries are: %+v", got.Beneficiaries)
			for _, ben := range got.Beneficiaries {
				if "0912141679" == ben.Data {
					match = true
				}

			}
			if !match {
				t.Errorf("no matching data was found")
			}
		})
	}
}

func TestGetUserByCard(t *testing.T) {
	type args struct {
		pan string
		db  *gorm.DB
	}
	tests := []struct {
		name    string
		args    args
		want    User
		wantErr bool
	}{
		{"get user with cards", args{pan: "23232", db: testDB}, User{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetUserByCard(tt.args.pan, tt.args.db)
			if err != nil {
				t.FailNow()
			}
			if got.ID != 1 {
				t.Errorf("GetUserByCard() = %v, want %v", got.ID, tt.want.ID)
			}
		})
	}
}
