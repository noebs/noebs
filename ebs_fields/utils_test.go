package ebs_fields

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUpdateUserWithKYC(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open("../test.db"), &gorm.Config{})
	if err := db.AutoMigrate(&User{}, &KYC{}, &Passport{}); err != nil {
		panic("failed to connect database" + err.Error())
	}

	// Create test data for KYC and Passport
	// kycData := KYC{Selfie: "Selfie_URL", PassportImg: "PassportImg_URL"}
	passportData := Passport{
		BirthDate:      time.Now(),
		IssueDate:      time.Now(),
		ExpirationDate: time.Now().AddDate(10, 0, 0),
		NationalNumber: "123456",
		PassportNumber: "ABC123",
		Gender:         "M",
		Nationality:    "X",
		HolderName:     "John Doe",
		Mobile:         "0111493885",
	}

	tests := []struct {
		name    string
		db      *gorm.DB
		Request KYCPassport
		wantErr bool
	}{
		{"Valid user", db, KYCPassport{Passport: passportData}, false},
		{"invalid foreignkey", db, KYCPassport{Passport: passportData}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := UpdateUserWithKYC(tt.db, &tt.Request)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddKYCAndPassportToUser() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				var kyc KYC
				if err := db.Preload("Passport").First(&kyc, "mobile = ?", tt.Request.Mobile).Error; err != nil {
					t.Errorf("Expected to find KYC for user %s, but got error %v", tt.Request.Mobile, err)
				}
			}
		})
	}
}
