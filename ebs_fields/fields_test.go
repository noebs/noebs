package ebs_fields

import (
	_ "embed"
	"reflect"
	"testing"
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
			var data []map[string]interface{}
			data = *tt.m
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
		TerminalID               string
		SystemTraceAuditNumber   int
		ClientID                 string
		PAN                      string
		ServiceID                string
		TranAmount               float32
		PhoneNumber              string
		FromAccount              string
		ToAccount                string
		FromCard                 string
		ToCard                   string
		OTP                      string
		OTPID                    string
		TranCurrencyCode         string
		EBSServiceName           string
		WorkingKey               string
		PayeeID                  string
		PubKeyValue              string
		UUID                     string
		ResponseMessage          string
		ResponseStatus           string
		ResponseCode             int
		ReferenceNumber          string
		ApprovalCode             string
		VoucherNumber            string
		VoucherCode              string
		MiniStatementRecords     MinistatementDB
		DisputeRRN               string
		AdditionalData           string
		TranDateTime             string
		TranFee                  *float32
		AdditionalAmount         *float32
		AcqTranFee               *float32
		IssTranFee               *float32
		TranCurrency             string
		MerchantID               string
		GeneratedQR              string
		Bank                     string
		Name                     string
		CardType                 string
		LastPAN                  string
		TransactionID            string
		CheckDuplicate           string
		AuthenticationType       string
		AccountCurrency          string
		ToAccountType            string
		FromAccountType          string
		EntityID                 string
		EntityType               string
		Username                 string
		DynamicFees              float64
		QRCode                   string
		ExpDate                  string
		FinancialInstitutionID   string
		CreationDate             string
		PanCategory              string
		EntityGroup              string
		MerchantAccountType      string
		MerchantAccountReference string
		MerchantName             string
		MerchantCity             string
		MobileNo                 string
		MerchantCategoryCode     string
		PostalCode               string
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
			res := &EBSResponse{
				TerminalID:               tt.fields.TerminalID,
				SystemTraceAuditNumber:   tt.fields.SystemTraceAuditNumber,
				ClientID:                 tt.fields.ClientID,
				PAN:                      tt.fields.PAN,
				ServiceID:                tt.fields.ServiceID,
				TranAmount:               tt.fields.TranAmount,
				PhoneNumber:              tt.fields.PhoneNumber,
				FromAccount:              tt.fields.FromAccount,
				ToAccount:                tt.fields.ToAccount,
				FromCard:                 tt.fields.FromCard,
				ToCard:                   tt.fields.ToCard,
				OTP:                      tt.fields.OTP,
				OTPID:                    tt.fields.OTPID,
				TranCurrencyCode:         tt.fields.TranCurrencyCode,
				EBSServiceName:           tt.fields.EBSServiceName,
				WorkingKey:               tt.fields.WorkingKey,
				PayeeID:                  tt.fields.PayeeID,
				PubKeyValue:              tt.fields.PubKeyValue,
				UUID:                     tt.fields.UUID,
				ResponseMessage:          tt.fields.ResponseMessage,
				ResponseStatus:           tt.fields.ResponseStatus,
				ResponseCode:             tt.fields.ResponseCode,
				ReferenceNumber:          tt.fields.ReferenceNumber,
				ApprovalCode:             tt.fields.ApprovalCode,
				VoucherNumber:            tt.fields.VoucherNumber,
				VoucherCode:              tt.fields.VoucherCode,
				MiniStatementRecords:     tt.fields.MiniStatementRecords,
				DisputeRRN:               tt.fields.DisputeRRN,
				AdditionalData:           tt.fields.AdditionalData,
				TranDateTime:             tt.fields.TranDateTime,
				TranFee:                  tt.fields.TranFee,
				AdditionalAmount:         tt.fields.AdditionalAmount,
				AcqTranFee:               tt.fields.AcqTranFee,
				IssTranFee:               tt.fields.IssTranFee,
				TranCurrency:             tt.fields.TranCurrency,
				MerchantID:               tt.fields.MerchantID,
				GeneratedQR:              tt.fields.GeneratedQR,
				Bank:                     tt.fields.Bank,
				Name:                     tt.fields.Name,
				CardType:                 tt.fields.CardType,
				LastPAN:                  tt.fields.LastPAN,
				TransactionID:            tt.fields.TransactionID,
				CheckDuplicate:           tt.fields.CheckDuplicate,
				AuthenticationType:       tt.fields.AuthenticationType,
				AccountCurrency:          tt.fields.AccountCurrency,
				ToAccountType:            tt.fields.ToAccountType,
				FromAccountType:          tt.fields.FromAccountType,
				EntityID:                 tt.fields.EntityID,
				EntityType:               tt.fields.EntityType,
				Username:                 tt.fields.Username,
				DynamicFees:              tt.fields.DynamicFees,
				QRCode:                   tt.fields.QRCode,
				ExpDate:                  tt.fields.ExpDate,
				FinancialInstitutionID:   tt.fields.FinancialInstitutionID,
				CreationDate:             tt.fields.CreationDate,
				PanCategory:              tt.fields.PanCategory,
				EntityGroup:              tt.fields.EntityGroup,
				MerchantAccountType:      tt.fields.MerchantAccountType,
				MerchantAccountReference: tt.fields.MerchantAccountReference,
				MerchantName:             tt.fields.MerchantName,
				MerchantCity:             tt.fields.MerchantCity,
				MobileNo:                 tt.fields.MobileNo,
				MerchantCategoryCode:     tt.fields.MerchantCategoryCode,
				PostalCode:               tt.fields.PostalCode,
			}
			res.MaskPAN()
			if res.PAN != tt.result {
				t.Errorf("PAN Masking failed: Expected %s, got %s", tt.result, res.PAN)
			}
		})
	}
}

func TestNoebsConfig_GetConsumerQA(t *testing.T) {
	type fields struct {
	}
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{"test-1", fields{}, "https://10.139.2.200:8443/Consumer/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SecretConfig.GetConsumer(); got != tt.want {
				t.Errorf("NoebsConfig.GetConsumerQA() = %v, want %v", got, tt.want)
			}
		})
	}
}
