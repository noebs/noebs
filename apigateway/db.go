package gateway

import (
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

type Service struct {
	gorm.Model
	ServiceName string `gorm:"unqiue_index"`
	Password    string
	JWT         JWT
	JWTID       int
}

type JWT struct {
	gorm.Model
	SecretKey string
}
