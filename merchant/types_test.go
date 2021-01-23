package merchant

import (
	"reflect"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/go-redis/redis/v7"
	"github.com/jinzhu/gorm"
	"github.com/sirupsen/logrus"
)

func TestMerchant_ByID(t *testing.T) {

	var db, _ = utils.Database("sqlite3", "test.db")

	type fields struct {
		Model       gorm.Model
		Merchant    ebs_fields.Merchant
		db          *gorm.DB
		log         *logrus.Logger
		redisClient *redis.Client
	}
	type args struct {
		id string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *ebs_fields.Merchant
		wantErr bool
	}{
		{"getting a result", fields{db: db}, args{id: "assd"}, &ebs_fields.Merchant{BillerID: "asd", MerchantMobileNumber: "0912141679"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Merchant{
				Model:    tt.fields.Model,
				Merchant: tt.fields.Merchant,
				db:       tt.fields.db,
				log:      tt.fields.log,
			}
			got, err := m.ByID(tt.args.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("Merchant.ByID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got.MerchantMobileNumber, tt.want.MerchantMobileNumber) {
				t.Errorf("Merchant.ByID() = %v, want %v", got, tt.want)
			}
		})
	}
}
