package ebs_fields

import "gorm.io/gorm"

// GetUserWithKYCAndPassport retrieves a User model from the database by mobile number, along with its associated KYC and Passport models.
// It uses the GORM Preload method to load the related KYC and Passport data in a single database query (eager loading).
// This helps reduce the number of queries to the database and can improve performance.
//
// Parameters:
// - db: An open database connection from GORM. This connection is used to execute the database queries.
// - mobile: The mobile number of the user to retrieve. This is used to search the User model in the database.
//
// Returns:
// - A pointer to the User model if the user is found, or nil if an error occurs or the user is not found.
// - An error if there is an issue during the retrieval process (such as a database error), or nil if no errors occur.
//
// The returned User model includes its associated KYC and Passport models (if they exist). If the user does not have associated KYC or Passport data, the corresponding fields in the User model are nil.
// If the user does not exist in the database, the function returns an error.
//
// Example usage:
// user, err := GetUserWithKYCAndPassport(db, "0111493885")
//
//	if err != nil {
//	    log.Fatalf("Error retrieving user: %v", err)
//	}
//
// fmt.Printf("User: %+v", user)
//
// This will output the User model with its associated KYC and Passport models (if they exist).
func GetUserWithKYCAndPassport(db *gorm.DB, mobile string) (*User, error) {
	var user User
	if err := db.Preload("KYC").Preload("KYC.Passport").Where("mobile = ?", mobile).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func UpdateUserWithKYC(db *gorm.DB, kycRequest *KYCPassport) error {
	// Find the user
	var user User
	result := db.First(&user, "mobile = ?", kycRequest.Mobile)
	if result.Error != nil {
		return result.Error
	}

	// Create the KYC record
	kyc := KYC{
		UserMobile:  kycRequest.Mobile,
		Mobile:      kycRequest.Mobile,
		Passport:    kycRequest.Passport,
		Selfie:      kycRequest.Selfie,
		PassportImg: kycRequest.PassportImg,
	}

	// Save the KYC record
	result = db.Create(&kyc)
	if result.Error != nil {
		return result.Error
	}

	// Update the user with the KYC details
	// user.KYC = &kyc
	// result = db.Save(&user)
	// if result.Error != nil {
	// 	return result.Error
	// }

	return nil
}
