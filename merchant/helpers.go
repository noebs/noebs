package merchant

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func validateRequest(v validator.ValidationErrors) ebs_fields.ErrorDetails {
	var details []ebs_fields.ErrDetails
	for _, err := range v {
		details = append(details, ebs_fields.ErrorToString(err))
	}
	payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
	return payload
}

func generateUUID() string {
	return uuid.New().String()
}

func handleChan(ctx context.Context, r *redis.Client) {
	// when getting redis results, ALWAYS json.Marshal them
	for {
		select {
		case c := <-billChan:
			if c.PayeeID == necPayment {
				var m necBill
				//FIXME there is a bug here
				//mapFields, _ := additionalFieldsToHash(c.BillInfo)
				m.NewFromMap(c.BillInfo)
				r.HSet(ctx, "meters", m.MeterNumber, m.CustomerName)
			}
			//} else if c.PayeeID == mtnTopUp {
			//	var m mtnBill
			//	mapFields, _ := additionalFieldsToHash(c.AdditionalData)
			//	m.NewFromMap(mapFields)
			//} else if c.PayeeID == sudaniTopUp {
			//	var m sudaniBill
			//	mapFields, _ := additionalFieldsToHash(c.AdditionalData)
			//	m.NewFromMap(mapFields)
			//}
		}
	}
}

const (
	zainBillInquiry      = "0010010002"
	zainBillPayment      = "0010010002"
	zainTopUp            = "0010010001"
	mtnBillInquiry       = "0010010004"
	mtnBillPayment       = "0010010004"
	mtnTopUp             = "0010010003"
	necPayment           = "0010020001"
	sudaniInquiryPayment = "0010010006"
	sudaniBillPayment    = "0010010006"
	sudaniTopUp          = "0010030002"
	moheBillInquiry      = "0010030002"
	moheBillPayment      = "0010030002"
	customsBillInquiry   = "0010030003"
	customsBillPayment   = "0010030003"
	moheArabBillInquiry  = "0010030004"
	moheArabBillPayment  = "0010030004"
	e15BillInquiry       = "0010050001"
	e15BillPayment       = "0010050001"
)

type necBill struct {
	SalesAmount  float64 `json:"SalesAmount"`
	FixedFee     float64 `json:"FixedFee"`
	Token        string  `json:"Token"`
	MeterNumber  string  `json:"MeterNumber"`
	CustomerName string  `json:"CustomerName"`
}

func (n *necBill) MarshalBinary() (data []byte, err error) {
	d, err := json.Marshal(n)
	return d, err
}

func (n *necBill) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, n)
}

func (n *necBill) NewFromMap(f map[string]interface{}) {
	/*
	   "accountNo": "AM042111907231",
	   "customerName": "ALSAFIE BAKHIEYT HEMYDAN",
	   "meterFees": "0",
	   "meterNumber": "04203594959",
	   "netAmount": "10",
	   "opertorMessage": "Credit Purchase",
	   "token": "07246305192693082213",
	   "unitsInKWh": "66.7",
	   "waterFees": "0.00"
	*/
	n.SalesAmount, _ = strconv.ParseFloat(f["netAmount"].(string), 32)
	n.CustomerName = f["customerName"].(string)
	n.FixedFee, _ = strconv.ParseFloat(f["meterFees"].(string), 32)
	n.MeterNumber = f["meterNumber"].(string)
	n.Token = f["token"].(string)
}
