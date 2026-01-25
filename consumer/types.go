package consumer

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

const (
	SPECIAL_BILLERS = "noebs:billers"
	KEY             = "publickey_"
)

type specialPaymentQueries struct {
	ID       string `form:"id,omitempty" binding:"required"`    //biller specific ids
	Token    string `form:"token,omitempty" binding:"required"` //noebs payment token
	IsJSON   bool   `form:"json,omitempty"`
	Referer  string `form:"to,default=https://sahil2.soluspay.net"`
	HooksURL string `form:"hooks,default=https://sahil2.soluspay.net"`
}

type cashoutFields struct {
	Name     string   `json:"name,omitempty" binding:"required"`
	Endpoint string   `json:"endpoint,omitempty" binding:"required"`
	Consent  bool     `json:"consent,omitempty"`
	Pan      string   `json:"pan,omitempty"`
	ExpDate  string   `json:"expDate,omitempty"`
	Ipin     string   `json:"ipin,omitempty"`
	Amount   int      `json:"amount,omitempty"`
	Biller   response `json:"details,omitempty"` // this is to embed ebs response inside the cashout. Could be a terrible idea
}

type billerForm struct {
	EBS          ebs_fields.EBSResponse `json:"ebs_response"`
	ID           string                 `json:"id"`
	IsSuccessful bool                   `json:"is_successful"`
	Token        string                 `json:"payment_token"`
	to           string
}

func (bf *billerForm) MarshalBinary() ([]byte, error) {
	return json.Marshal(bf)
}

func (bf *billerForm) UnmarshalBinary(data []byte) error {
	// convert data to yours, let's assume its json data
	return json.Unmarshal(data, bf)
}

func notEbs(pan string) bool {
	/*
		Bank Code        Bank Card PREFIX        Bank Short Name        Bank Full name
		2                    639186                      FISB                                 Faisal Islamic Bank
		4                    639256                      BAKH                                  Bank of Khartoum
		16                    639184                       RAKA                                  Al Baraka Sudanese Bank
		30                    639330                       ALSA                                  Al Salam Bank
	*/

	re := regexp.MustCompile(`(^639186|^639256|^639184|^639330)`)
	return re.Match([]byte(pan))
}

type cashout struct {
	Amount int    `json:"amount" binding:"required"`
	ID     string `json:"id" binding:"required"`
	Card   string `json:"pan"`
}

type paymentResponse struct {
	TransactionID string `json:"transaction_id"`
	ebs_fields.EBSResponse
}

func (pr *paymentResponse) MarshalBinary() ([]byte, error) {
	return json.Marshal(pr)
}

func (pr *paymentResponse) UnmarshalBinary(data []byte) error {
	// convert data to yours, let's assume its json data
	return json.Unmarshal(data, pr)
}

type validationError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

func (ve *validationError) marshal() []byte {
	d, _ := json.Marshal(ve)
	return d
}

type response struct {
	Response string    `json:"response"`
	Code     int       `json:"code"`
	Time     time.Time `json:"time"`
	Amount   int       `json:"amount"`
}

type genErr struct {
	Message string                 `json:"message,omitempty"`
	Code    int                    `json:"code,omitempty"`
	Status  string                 `json:"status,omitempty"`
	Details ebs_fields.EBSResponse `json:"details,omitempty"`
}

func newFromBytes(d []byte, code int) (response, error) {
	if code == 200 {
		var dd map[string]ebs_fields.EBSParserFields
		if err := json.Unmarshal(d, &dd); err != nil {
			return response{}, err
		}
		return response{Code: dd["ebs_response"].ResponseCode,
			Response: dd["ebs_response"].ResponseMessage,
			Time:     time.Time{},
			Amount:   int(dd["ebs_response"].TranAmount),
		}, nil
		// now we gonna parse the response somewhere
	} else if code == 400 {
		return response{
			Code:     69,
			Response: "Generic Error",
		}, nil
	} else if code == 502 {
		var dd genErr
		if err := json.Unmarshal(d, &dd); err != nil {
			return response{}, err
		}
		c := dd.Details.ResponseCode
		m := dd.Details.ResponseMessage
		return response{Code: c,
			Response: m,
			Time:     time.Time{},
			Amount:   0}, nil

	} else {
		return response{
			Code:     69,
			Response: "Generic Error",
		}, nil
	}
}

// PushData is a notification payload persisted in the store.
type PushData = ebs_fields.PushDataRecord

// various consts we are using for push data and notifications
const (
	EBS_NOTIFICATION       = "ebs"
	NOEBS_NOTIFICATION     = "noebs"
	MARKETING_NOTIFICATION = "marketing"
	OTHERS_NOTIFICATIONS   = "others"
	CTA_CARD_TRANSFER      = "card_transfer"
	CTA_BALANCE            = "balance"
	CTA_BILL_PAYMENT       = "bill_payment"
	CTA_VOUCHER            = "voucher"
	CTA_REQUEST_FUNDS      = "request_funds"
	CTA_OTHERS             = "others"
)
