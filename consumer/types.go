package consumer

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
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

type redisCard map[string]interface{}

//cardsFromZ marshals []string to []ebs_fields.CardsRedis
func cardsFromZ(cards []string) []ebs_fields.CardsRedis {
	var cb ebs_fields.CardsRedis
	var cardBytes []ebs_fields.CardsRedis
	for k, v := range cards {
		json.Unmarshal([]byte(v), &cb)
		cb.ID = k + 1
		cardBytes = append(cardBytes, cb)
	}
	return cardBytes
}

//cardsFromZ marshals []string to []ebs_fields.CardsRedis
func cardsFromZNoIDS(cards []string) []ebs_fields.CardsRedis {
	var cb ebs_fields.CardsRedis
	var cardBytes []ebs_fields.CardsRedis
	for _, v := range cards {
		json.Unmarshal([]byte(v), &cb)
		cb.ID = 0
		cardBytes = append(cardBytes, cb)
	}
	return cardBytes
}

func generateCardsIds(c *[]ebs_fields.CardsRedis) {
	for id, card := range *c {
		card.ID = id + 1
	}
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

func pushMessage(content string, pushID ...string) {
	/*
		curl --include --request POST --header "Content-Type: application/json; charset=utf-8"
		 -H "Authorization: Basic NjEwNjk1YzctYzZjZC00Yzg2LTk5ZjYtMzI2ZjViZjE2ZTdi" -d
		  '{ "app_id": "20a9520e-44fd-4657-a2d9-78f5063045aa",
		  "include_player_ids": ["a180bc8b-6b56-405e-ae77-dc055d86a9df"],
		  "channel_for_external_user_ids": "push",
		"data": {"foo": "bar"},
		  "contents": {"en": "Let us work it here!"} }'
		 https://onesignal.com/api/v1/notifications
	*/
	pushID = []string{"a180bc8b-6b56-405e-ae77-dc055d86a9df"}
	b := map[string]interface{}{
		"app_id":                        "20a9520e-44fd-4657-a2d9-78f5063045aa",
		"include_player_ids":            pushID, // "a180bc8b-6b56-405e-ae77-dc055d86a9df"
		"channel_for_external_user_ids": "push",
		"data":                          map[string]string{"foo": "bar"},
		"contents":                      map[string]string{"en": content},
	}
	data, _ := json.Marshal(&b)
	log.Printf("the data is: %v", string(data))
	client, err := http.NewRequest("POST", "https://onesignal.com/api/v1/notifications", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("error in sending a request: %v", err)
		return
	}

	client.Header.Set("Content-Type", "application/json; charset=utf-8")
	client.Header.Set("Authorization", "Basic NjEwNjk1YzctYzZjZC00Yzg2LTk5ZjYtMzI2ZjViZjE2ZTdi")
	res, err := http.DefaultClient.Do(client)
	if err != nil {
		log.Printf("Error in parse: %v", err)
		return
	}

	d, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Printf("Error in parse: %v", err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		log.Printf("Response status is: %v - response is: %v", res.StatusCode, string(d))
		return
	}
	log.Printf("Response status is: %v - response is: %v", res.StatusCode, string(d))

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

type SMS struct {
	Mobile  string `json:"mobile,omitempty"`
	Message string `json:"message,omitempty"`
}
