// Package merchant represents our merchant apis and especially types.
package merchant

import (
	"log"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

//Merchant is a fully qualified noebs merchant
type Merchant struct {
	gorm.Model
	ebs_fields.Merchant

	db  *gorm.DB
	log *logrus.Logger
}

//Init populates merchant with db pointer
func (m *Merchant) Init(db *gorm.DB, log *logrus.Logger) {
	m.db = db
	m.log = log
	m.db.AutoMigrate(m)
}

//SetDB assigns db to merchant instance
func (m *Merchant) SetDB(db *gorm.DB) {
	m.db = db
}

//New generates a new merchant struct to be used later
func New(db *gorm.DB) *Merchant {
	return &Merchant{db: db}
}

//ByID get a merchant profile using their id
func (m *Merchant) ByID(id string) (*ebs_fields.Merchant, error) {
	m.db.AutoMigrate(m)

	var res ebs_fields.Merchant

	if err := m.db.Where("merchant_id = ?", id).Find(&res).Error; err != nil {
		return nil, err
	}
	return &res, nil

}

func (m *Merchant) encryptPassword() error {
	password, err := bcrypt.GenerateFromPassword([]byte(m.Password), 0)
	if err != nil {
		return err
	}
	m.Password = string(password)
	return nil
}

func (m *Merchant) authenticate(password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(password), []byte(m.Password)); err != nil {
		return err
	}
	return nil
}

func (m *Merchant) Write() error {
	if err := m.db.AutoMigrate(m).Error; err != nil {
		log.Printf("error in migration: %v", err)
		// return err
	}

	if err := m.db.Create(m).Error; err != nil {
		// m.log.Printf("error in writing model: :%v", err)
		return err
	}
	return nil

}

func (m *Merchant) get(id string) (Merchant, error) {
	var merchant Merchant
	if err := m.db.Where("merchant_id = ?", id).First(&merchant).Error; err != nil {
		return merchant, err
	}
	return merchant, nil
}

func (m *Merchant) getMobile(id string) (Merchant, error) {
	var merchant Merchant
	if err := m.db.Where("mobile = ?", id).First(&merchant).Error; err != nil {
		return merchant, err
	}
	return merchant, nil
}
