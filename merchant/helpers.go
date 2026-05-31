package merchant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
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

var ErrInvalidBillInfo = errors.New("invalid bill info")

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

func (n *necBill) NewFromMap(f map[string]interface{}) error {
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
	salesAmount, err := requiredBillFloat(f, "netAmount")
	if err != nil {
		return err
	}
	customerName, err := requiredBillString(f, "customerName")
	if err != nil {
		return err
	}
	fixedFee, err := requiredBillFloat(f, "meterFees")
	if err != nil {
		return err
	}
	meterNumber, err := requiredBillString(f, "meterNumber")
	if err != nil {
		return err
	}
	token, err := requiredBillString(f, "token")
	if err != nil {
		return err
	}

	n.SalesAmount = salesAmount
	n.CustomerName = customerName
	n.FixedFee = fixedFee
	n.MeterNumber = meterNumber
	n.Token = token
	return nil
}

func requiredBillString(fields map[string]interface{}, key string) (string, error) {
	value, ok := fields[key]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrInvalidBillInfo, key)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%w: %s", ErrInvalidBillInfo, key)
	}
	return text, nil
}

func requiredBillFloat(fields map[string]interface{}, key string) (float64, error) {
	value, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrInvalidBillInfo, key)
	}
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %s", ErrInvalidBillInfo, key)
		}
		return parsed, nil
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("%w: %s", ErrInvalidBillInfo, key)
		}
		return parsed, nil
	case float64:
		return typed, nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrInvalidBillInfo, key)
	}
}
