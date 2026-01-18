package ebs_fields

import (
	"crypto/rand"
	"encoding/base64"
	"reflect"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var testDB, err = gorm.Open(sqlite.Open("../test.db"), &gorm.Config{})

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

func TestGetMobiles(t *testing.T) {
	type args struct {
		pan string
		db  *gorm.DB
	}
	testDB = testDB.Debug()
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{"get_mobiles", args{"7222331370182156067", testDB}, []string{"322", "2", "3", "4"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetDeviceIDsByPan(tt.args.pan, tt.args.db)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetMobiles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetMobiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func setup() *gorm.DB {
	db, err := gorm.Open(sqlite.Open("../test.db"))
	if err != nil {
		panic("failed to connect database")
	}
	// Migrate the schema
	err = db.AutoMigrate(&User{}, &KYC{}, &Passport{})
	if err != nil {
		panic(err)
	}

	return db
}

// generateRandomBytes returns securely generated random bytes.
func generateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// generateRandomBase64String returns a base64 encoded securely generated random string.
func generateRandomBase64String(s int) string {
	b, _ := generateRandomBytes(s)
	return base64.StdEncoding.EncodeToString(b)
}

func TestCreateKYCAndPassportForExistingUser(t *testing.T) {
	db := setup()

	// Define test cases
	testCases := []struct {
		name          string
		userMobile    string
		selfie        string
		passportImg   string
		passport      Passport
		expectedError bool
		kycMobile     string
	}{
		{
			name:        "Valid User",
			userMobile:  "0111493885",
			kycMobile:   "011149387785",
			selfie:      generateRandomBase64String(32),
			passportImg: generateRandomBase64String(32),
			passport: Passport{
				Mobile:         "011149387785",
				BirthDate:      time.Now(),
				IssueDate:      time.Now(),
				ExpirationDate: time.Now(),
				NationalNumber: "1234567890",
				PassportNumber: "XYZ1234567",
				Gender:         "Male",
				Nationality:    "Exampleland",
				HolderName:     "John Doe",
			},
			expectedError: false,
		},
		// Add more test cases as needed...
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Check if user exists
			var user User
			if err := db.First(&user, "mobile = ?", tc.userMobile).Error; err != nil {
				t.Errorf("User does not exist: %v", err)
				return
			}

			// Create a new KYC
			kyc := KYC{
				Mobile:      tc.kycMobile,
				Passport:    tc.passport,
				Selfie:      tc.selfie,
				PassportImg: tc.passportImg,
				UserMobile:  tc.userMobile,
			}

			err := db.Create(&kyc).Error
			if (err != nil) != tc.expectedError {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Retrieve the user with KYC and Passport
			var retrievedUser User
			db.Preload("KYC").Preload("KYC.Passport").First(&retrievedUser, "mobile = ?", tc.kycMobile)

			// Check if the passport data is correct
			if retrievedUser.KYC.Passport.HolderName != tc.passport.HolderName {
				t.Errorf("Failed to create passport with correct data")
			}

			// Check if the KYC data is correct
			if retrievedUser.KYC.Selfie != tc.selfie {
				t.Errorf("Failed to create KYC with correct data")
			}
		})
	}
}

func TestGetUserWithKYCAndPassport(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	db.AutoMigrate(&User{}, &KYC{}, &Passport{})

	// Create a user with associated KYC and Passport
	passport := &Passport{
		Mobile:         "0111493885",
		BirthDate:      time.Now(),
		IssueDate:      time.Now(),
		ExpirationDate: time.Now(),
		NationalNumber: "123456",
		PassportNumber: "ABC123",
		Gender:         "M",
		Nationality:    "X",
		HolderName:     "John Doe",
	}
	kyc := &KYC{
		Mobile:      "0111493885",
		UserMobile:  "0111493885",
		Passport:    *passport,
		Selfie:      "Selfie_URL",
		PassportImg: "PassportImg_URL",
	}
	user := &User{
		Created:         time.Now().Unix(),
		Password:        "$2a$08$T7DFFXzndRwZnzYSHSrQPesxr52aWOG76NGw.TdxMKg1jrxq5A2cy",
		Fullname:        "John Doe",
		Username:        "johndoe",
		Gender:          "M",
		Birthday:        "2000-01-01",
		Email:           "johndoe@example.com",
		IsMerchant:      false,
		PublicKey:       "PublicKey",
		DeviceID:        "DeviceID",
		OTP:             "OTP",
		SignedOTP:       "SignedOTP",
		FirebaseIDToken: "FirebaseIDToken",
		NewPassword:     "NewPassword123",
		IsPasswordOTP:   false,
		MainCard:        "MainCard",
		ExpDate:         "2022-12-31",
		Language:        "EN",
		IsVerified:      true,
		Mobile:          "0111493885",
		KYC:             kyc,
	}
	db.Create(user)

	userWithoutKYC := &User{Mobile: "0111493886"}
	db.Create(userWithoutKYC)

	dbWithError, _ := gorm.Open(sqlite.Open(":error:"), &gorm.Config{})

	tests := []struct {
		name    string
		db      *gorm.DB
		mobile  string
		wantErr bool
		user    *User
	}{
		{"User with KYC and Passport", db, "0111493885", false, user},
		{"User without KYC and Passport", db, "0111493886", false, userWithoutKYC},
		{"Non-existent user", db, "0000000000", true, nil},
		{"Database error", dbWithError, "0111493885", true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetUserWithKYCAndPassport(tt.db, tt.mobile)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetUserWithKYCAndPassport() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil && result.Password != tt.user.Password {
				t.Errorf("Expected user password to be %s, but got %s", tt.user.Password, result.Password)
			}
		})
	}

	t.Run("Check Password", func(t *testing.T) {
		result, err := GetUserWithKYCAndPassport(db, "0111493885")
		if err != nil {
			t.Errorf("GetUserWithKYCAndPassport() error = %v", err)
			return
		}

		if result.Password != user.Password {
			t.Errorf("Expected user password to be %s, but got %s", user.Password, result.Password)
		}
	})
}
