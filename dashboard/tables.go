package dashboard

import (
	"github.com/jinzhu/gorm"
	"log"
)

// what do i really want to do?

func dbConnect() {
	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&Transaction{}); err != nil {
		log.Fatalf("there is an error in migration %v", err.Error)
	}
	// you should also commit the results here..., and that has to be done per "endpoint"!

}
