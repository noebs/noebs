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
	if err := db.AutoMigrate(&Token{}, &EBSResponse{}, &User{}); err != nil {
		panic(err)
	}
	if err := db.AutoMigrate(&User{}); err != nil {
		t.FailNow()
	}
	type args struct {
		transaction EBSResponse
		uuid        string
		db          *gorm.DB
	}
	res := EBSResponse{TranAmount: 696969, ResponseMessage: "Successful"}
	tests := []struct {
		name    string
		args    args
		wantErr string
	}{
		{"test_upsert_transaction", args{transaction: res, uuid: "461f5bb8-f9e8-42eb-9b15-2195e2e30101", db: db}, "Successful"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tran := tt.args.transaction
			var trans []EBSResponse
			trans = append(trans, tran)
			p := Token{
				db:           tt.args.db,
				EBSResponses: trans,
			}
			// p.UserID = 1
			// p.User = User{Mobile: "0912141679", Model: gorm.Model{ID: 1}}
			if err := p.UpsertTransaction(tt.args.transaction, tt.args.uuid); err != nil {
				t.Errorf("PaymentToken.UpsertTransaction() error = %v, wantErr %v", err, tt.wantErr)
			}
			// newToken, err := GetTokenWithTransaction(tt.args.uuid, tt.args.db)
			// if err != nil {
			// 	t.Errorf("GetTokenWithTransaction() error = %v, wantErr %v, the token: %+v", err, tt.wantErr, newToken)
			// }

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
		{"Test_to_card", args{"cbd7688f-2a60-439d-b53a-66eb04c060f1", "0912141666", testDB}, "working", false},
	}
	if err := testDB.AutoMigrate(&User{}, &Token{}); err != nil {
		t.FailNow()
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetTokenByUUID(tt.args.uuid, tt.args.db)
			if err != nil {
				t.Errorf("the error is: %v", err)
			}
			t.Logf("the token we got is: %+v", got)
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
		want    Token
		wantErr bool
	}{
		{"Test_to_card", args{"015b88da-1203-4a69-a3ef-e447b6df4ccc", testDB}, Token{}, false},
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
		{"get user with cards", args{pan: "7222331370182156067", db: testDB}, User{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetUserByCard(tt.args.pan, tt.args.db)
			if err != nil {
				t.FailNow()
			}
			if got.Mobile != "0923377628" {
				t.Errorf("GetUserByCard() = %v, want %v", got.ID, tt.want.ID)
			}
		})
	}
}

func TestUser_GenerateOTP(t *testing.T) {

	tests := []struct {
		name    string
		pubkey  string
		wantErr bool
	}{
		{"rest generate otp", "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAteM6IQBAUK4Lsb42zgr13YRHoBWyiQHuifjHvxxI7QHnOlQGRYU0xqgplV+Gumers6c3vH5xtlPsy6lHFJ7VQnTPHlZIcRefy7rKsVC+D1cjA6H3W6jWAdKDslxEb8sMfnatWI1PO0MNDz4Nh7KHS3V51nDqlx7I+TggtKZU8zq/epeVb+pqCKQphGd36J9KqZzaobDKxY6ObrLQDncKtF74UerJjmQxFd52VM/XDwOjmWS7shpQZx2HaLzFq6IOpTnKE+nySZqoXZVDB5j6llctinSs9E+HAOmN2r32B6zthYvMIO8gQjSZNyRp0E/GKhlPgfF8r55upszm7qIUZQIDAQAB", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := User{PublicKey: tt.pubkey}
			got, err := u.GenerateOtp()
			if (err != nil) != tt.wantErr {
				t.Errorf("User.GenerateOTP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			print(got)
			if len(got) != 6 {
				t.Error("User.GenerateOTP() error = wrong otp")
				return
			}
		})
	}
}

func TestUser_VerifyOtp(t *testing.T) {

	tests := []struct {
		name    string
		code    string
		pubkey  string
		wantErr bool
	}{
		{"rest generate otp", "464261", "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAteM6IQBAUK4Lsb42zgr13YRHoBWyiQHuifjHvxxI7QHnOlQGRYU0xqgplV+Gumers6c3vH5xtlPsy6lHFJ7VQnTPHlZIcRefy7rKsVC+D1cjA6H3W6jWAdKDslxEb8sMfnatWI1PO0MNDz4Nh7KHS3V51nDqlx7I+TggtKZU8zq/epeVb+pqCKQphGd36J9KqZzaobDKxY6ObrLQDncKtF74UerJjmQxFd52VM/XDwOjmWS7shpQZx2HaLzFq6IOpTnKE+nySZqoXZVDB5j6llctinSs9E+HAOmN2r32B6zthYvMIO8gQjSZNyRp0E/GKhlPgfF8r55upszm7qIUZQIDAQAB", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := User{PublicKey: tt.pubkey}
			if !u.VerifyOtp(tt.code) {
				t.Errorf("User.VerifyOtp() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
		})
	}
}

func TestExpandCard(t *testing.T) {
	type args struct {
		card      string
		userCards []Card
	}
	cards1 := []Card{{Pan: "92221212345678901234"}, {Pan: "9222121234567895678"}, {Pan: "9222121234567899999"}, {Pan: "2222121234567899999"}}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{"failure", args{"92221****8889998", cards1}, "", true},
		{"regular case of find a card with asterisks", args{"922212888888885678", cards1}, "9222121234567895678", false},
		{"ends with", args{"222212000008889999", cards1}, "2222121234567899999", false},
		{"nil user cards", args{"222212000008889999", nil}, "", true},
		{"nil query nil cards", args{"", nil}, "", true},
		{"nil query with cards", args{"", cards1}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandCard(tt.args.card, tt.args.userCards)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExpandCard() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExpandCard() = %v, want %v", got, tt.want)
			}
		})
	}
}
