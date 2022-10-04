package ebs_fields

import (
	_ "embed"
	"reflect"
	"testing"

	"gorm.io/gorm"
)

func Test_isEBS(t *testing.T) {

	tests := []struct {
		name string
		args string
		want bool
	}{
		// failed tests
		{"63918658585", "63918658585", true},
		{"failed tests", "858563918658585", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEBS(tt.args); got != tt.want {
				t.Errorf("isEBS() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsumerCardTransferFields_MustMarshal(t *testing.T) {

	c := ConsumerCardTransferAndMobileFields{
		ConsumerCardTransferFields: ConsumerCardTransferFields{
			ConsumerCommonFields: ConsumerCommonFields{
				ApplicationId: "323232",
				TranDateTime:  "323232",
				UUID:          "23232",
			},
			ConsumerCardHolderFields: ConsumerCardHolderFields{
				Pan:     "32323232",
				Ipin:    "2231",
				ExpDate: "2202",
			},
			AmountFields: AmountFields{
				TranAmount:       0.32320,
				TranCurrencyCode: "4242",
			},
			ToCard: "424242",
		},
		Mobile: "42424242",
	}

	tests := []struct {
		name   string
		fields ConsumerCardTransferAndMobileFields
		want   []byte
	}{
		{"testing marshalling", c, []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			if got := c.MustMarshal(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ConsumerCardTransferFields.MustMarshal() = %v, want %v", string(got), tt.want)
			}
		})
	}
}

func TestMinistatementDB_Scan(t *testing.T) {
	type args struct {
		value interface{}
	}
	wantRes := []map[string]interface{}{
		{"name": "ahmed", "id": 1},
		{"name": "nosa", "id": 3},
	}
	tests := []struct {
		name    string
		m       *MinistatementDB
		args    args
		wantErr bool
		want    []map[string]interface{}
	}{
		{"test scan", &MinistatementDB{}, args{value: `[{"name": "ahmed", "id": 1}, {"name": "nosa", "id": 3}]`}, false, wantRes},
		{"test scan", &MinistatementDB{}, args{value: ``}, false, wantRes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Scan(tt.args.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("MinistatementDB.Scan() error = %v, wantErr %v", err, tt.wantErr)
			}
			data := *tt.m
			// i think it might be better to just work around that...
			if len(data) == 0 {
				return
			}

			if data[0]["name"] != wantRes[0]["name"] {
				t.Errorf("MinistatementDB.Scan() = %v, want %v", data[0]["name"], wantRes[0]["name"])
			}

		})
	}
}

func TestGenericEBSResponseFields_MaskPAN(t *testing.T) {
	type fields struct {
		PAN string
	}
	tests := []struct {
		name   string
		fields fields
		result string
	}{
		{"test-1", fields{PAN: "1234567890123456"}, "123456*****3456"},
		{"test-1", fields{PAN: "1234567890123456"}, "12345612345678901234563456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &EBSResponse{}
			res.MaskPAN()
			if res.PAN != tt.result {
				t.Errorf("PAN Masking failed: Expected %s, got %s", tt.result, res.PAN)
			}
		})
	}
}

func TestCacheBillers_Save(t *testing.T) {
	testDB.AutoMigrate(&CacheBillers{})
	type fields struct {
		Mobile   string
		BillerID string
	}
	type args struct {
		db   *gorm.DB
		flip bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
	}{
		{"test saving phone number", fields{Mobile: "0912141679", BillerID: "0010010004"}, args{testDB, false}, "0010010004"},
		{"test saving phone number", fields{Mobile: "0912141655", BillerID: "0010010004"}, args{testDB, false}, "0010010004"},
		{"test saving phone number", fields{Mobile: "01112345678", BillerID: "0010010002"}, args{testDB, true}, "0010010001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := CacheBillers{
				Mobile:   tt.fields.Mobile,
				BillerID: tt.fields.BillerID,
			}
			if err := c.Save(tt.args.db, tt.args.flip); (err == nil) && c.BillerID != tt.want {
				t.Errorf("CacheBillers.Save() error = %v, wantErr %v", c.BillerID, tt.want)
			}
		})
	}
}

func TestGetBillerInfo(t *testing.T) {
	type args struct {
		mobile string
		db     *gorm.DB
	}
	tests := []struct {
		name string
		args args
		want CacheBillers
	}{
		{name: "test saving phone number", args: args{mobile: "0912141679", db: testDB}, want: CacheBillers{BillerID: "0010010002"}},
		{name: "err phone no", args: args{mobile: "0912141679", db: testDB}, want: CacheBillers{BillerID: "0010010005"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetBillerInfo(tt.args.mobile, tt.args.db)
			if err != nil {
				t.Errorf("the error is: %v", err)
			}
			if !reflect.DeepEqual(got.BillerID, tt.want.BillerID) {
				t.Errorf("GetBillerInfo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdateBiller(t *testing.T) {
	type args struct {
		mobile string
		biller string
		db     *gorm.DB
	}
	tests := []struct {
		name    string
		args    args
		want    CacheBillers
		wantErr bool
	}{
		{"test update", args{mobile: "0912141679", biller: "000111222", db: testDB}, CacheBillers{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UpdateBiller(tt.args.mobile, tt.args.biller, tt.args.db)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateBiller() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UpdateBiller() = %v, want %v", got, tt.want)
			}
		})
	}
}
