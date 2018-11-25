package gateway

import (
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
)

type ServiceModel struct {
	gorm.Model
	ServiceID string `gorm:"unqiue_index"`
	Password  string
}

type JWTModel struct {
	gorm.Model
	Jwt       string
	ServiceModel
}
