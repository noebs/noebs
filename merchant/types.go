package merchant

import (
	"github.com/adonese/noebs/ebs_fields"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
)

//Merchant is a fully qualified noebs merchant
type Merchant struct {
	gorm.Model
	ebs_fields.Merchant
	db  *gorm.DB
	log *logrus.Logger
}

//New generates a new merchant struct to be used later
func New() *Merchant {
	return &Merchant{}
}

func (m *Merchant) write() error {

	if err := m.db.DB().Ping(); err != nil {
		m.log.Printf("Error in pinging: %v", err)
		return err
	}
	if err := m.db.AutoMigrate(m).Error; err != nil {
		m.log.Printf("error in writing model: :%v", err)
		return err
	}

	if err := m.db.Create(m).Error; err != nil {
		m.log.Printf("error in writing model: :%v", err)
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
