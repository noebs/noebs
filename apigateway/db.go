package gateway

import (
	_ "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type Service struct {
	gorm.Model
	ServiceName string `gorm:"index"`
	Password    string
	JWT         JWT
	JWTID       int
}

type JWT struct {
	gorm.Model
	SecretKey string
}
