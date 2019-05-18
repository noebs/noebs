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

	db.AutoMigrate(&dashboard.Transaction{})

	return db
}

type redisPurchaseFields map[string]interface{}
