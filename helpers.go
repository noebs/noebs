package main

import (
	"github.com/adonese/noebs/dashboard"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
)

func database(dialect string, fname string) *gorm.DB {
	db, err := gorm.Open(dialect, fname)

	if err != nil {
		log.WithFields(logrus.Fields{
			"error":   err.Error(),
			"details": "there's an error in connecting to DB",
		}).Info("there is an error in connecting to DB")
	}

	// why are we using env here?
	// clearly i'm using db variable directly

	if err := db.AutoMigrate(&dashboard.Transaction{}).Error; err != nil {
		log.WithFields(logrus.Fields{
			"error":   err.Error(),
			"details": "there's an error in connecting to DB",
		}).Info("there is an error in  DB")
	}

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}).Error; err != nil {
		log.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Info("Unable to migrate database")
	}
	return db
}
