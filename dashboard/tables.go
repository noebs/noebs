package dashboard

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// what do i really want to do?

func dbConnect() {
	db, err := gorm.Open(sqlite.Open("test.db"), &gorm.Config{})

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	if err := db.AutoMigrate(&Transaction{}); err != nil {
		log.Fatalf("there is an error in migration %v", err.Error)
	}
	// you should also commit the results here..., and that has to be done per "endpoint"!

}
