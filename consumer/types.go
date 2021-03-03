package consumer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

const (
	SPECIAL_BILLERS = "noebs:billers"
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
	EBS          ebs_fields.GenericEBSResponseFields `json:"ebs_response"`
	ID           string                              `json:"id"`
	IsSuccessful bool                                `json:"is_successful"`
	Token        string                              `json:"payment_token"`
	to           string
}

func (bf *billerForm) MarshalBinary() ([]byte, error) {
	return json.Marshal(bf)
}

func (bf *billerForm) UnmarshalBinary(data []byte) error {
	// convert data to yours, let's assume its json data
	return json.Unmarshal(data, bf)
}

type card map[string]interface{}

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

//FIXME #62 make sure to add redisClient here
type paymentTokens struct {
	Name        string  `json:"name,omitempty"`
	Amount      float32 `json:"amount,omitempty"`
	ID          string  `json:"id,omitempty"`
	UUID        string  `json:"uuid"`
	redisClient *redis.Client
	BillerID    string `json:"-"` // account number
	IsActive    bool   `json:"-"`
}

type paymentResponse struct {
	TransactionID string `json:"transaction_id"`
	ebs_fields.GenericEBSResponseFields
}

func (pr *paymentResponse) MarshalBinary() ([]byte, error) {
	return json.Marshal(pr)
}

func (pr *paymentResponse) UnmarshalBinary(data []byte) error {
	// convert data to yours, let's assume its json data
	return json.Unmarshal(data, pr)
}

//NewPayment generate a new payment token to be used by consumers
func NewPayment(r *redis.Client) *paymentTokens {
	return &paymentTokens{redisClient: r}
}

func (p *paymentTokens) checkUUID(id string, redisClient *redis.Client) (bool, validationError) {
	// return true, validationError{}

	if _, err := p.fromRedis(id); err != nil {
		ve := validationError{Message: err.Error(), Code: "payment_token_not_found"}
		return false, ve
	}
	return true, validationError{}
}

func (p *paymentTokens) check(id string, amount float32, redisClient *redis.Client) (bool, validationError) {
	// return true, validationError{}

	if _, err := p.fromRedis(id); err != nil {
		ve := validationError{Message: err.Error(), Code: "payment_token_not_found"}
		return false, ve
	}

	if !p.validate(id, amount) {
		ve := validationError{Message: "Wrong payment info. Amount and Payment ID doesn't match existing records", Code: "mismatched_special_payment_data"}
		return false, ve
	}
	return true, validationError{}
}

func (p *paymentTokens) getUUID() string {
	if p.UUID != "" {
		return p.UUID
	}
	id := uuid.New().String()
	p.UUID = id
	return id
}

func (p *paymentTokens) validate(id string, amount float32) bool {
	log.Printf("Given: ID: %s - Amount: %f\nWanted: %s - Amount: %f", id, amount, p.ID, p.Amount)
	return p.Amount == amount
}

func (p *paymentTokens) toMap() map[string]interface{} {
	res := map[string]interface{}{
		"id":     p.ID,
		"amount": p.Amount,
		"name":   p.Name,
	}
	return res
}

func (p *paymentTokens) fromMap(m map[string]interface{}) {
	p.Amount = m["amount"].(float32)
	p.ID = m["id"].(string)
	p.Name = m["name"].(string)
}

func (p *paymentTokens) new() error {
	p.ID = p.getUUID()
	return nil

}

//storeKey stores a uuid (from a biller form) to our redis for 30 minutes.
func (p *paymentTokens) storeKey() (string, error) {

	// tt := 30 * time.Minute
	tt := 30 * time.Minute
	log.Printf("the key we are storing is: %v", p.ID)
	log.Printf("The key is: %v", p.ID)

	if err := p.redisClient.Set("key_"+p.ID, p.ID, tt).Err(); err != nil {

		return "", err
	}
	return p.ID, nil

}

//getKey used to enforce timeouts. It returns nil (and error) if the key is timedout
func (p *paymentTokens) getKey(key string) (string, error) {

	if r, err := p.redisClient.Get("key_" + key).Result(); err != nil {
		log.Printf(r)
		return "", err
	} else {
		if r != "" {
			return r, nil
		}
		return r, errors.New("empty_res")
	}

}

//toRedis stores a payment token instance to a redis store
func (p *paymentTokens) toRedis() error {

	h := p.toMap()
	if _, err := p.redisClient.HMSet(p.ID, h).Result(); err != nil {
		return err
	}
	return nil

}

//exists check if namespace is stored in our system
// this doesn't work
func (p *paymentTokens) exists(namespace string) bool {

	if _, err := p.redisClient.SMembers(namespace).Result(); err != nil {
		log.Printf("Error in exists: %v", err)
		return false
	}
	return true
}

//newBiller adds a new biller's namespace to noebs:billers `set`
func (p *paymentTokens) newBiller(namespace string) error {
	if _, err := p.redisClient.SAdd(SPECIAL_BILLERS, namespace).Result(); err != nil {
		log.Printf("Error in newBiller: %v", err)
		return err
	}
	return nil
}

//id2owner adds a payment token to a biller's SET transactions
func (p *paymentTokens) id2owner(namespace string) error {

	h := p.toMap()
	if namespace == "" {
		namespace = "sahil_wallet"
	}
	if _, err := p.redisClient.SAdd(namespace, h).Result(); err != nil {

		return err
	}
	return nil

}

func (p *paymentTokens) isAuthorized(token string) error {
	return p.getToken(p.BillerID, token)
}

//addToken adds token to a biller ID biller_id:tokens set
func (p *paymentTokens) addToken(provider, token string) error {
	if err := p.redisClient.SAdd(provider+":tokens", token).Err(); err != nil {
		log.Printf("error in adding Token: %v", err)
		return err
	}
	return nil
}

//getToken checks if a token is assigned to a particular biller ID
func (p *paymentTokens) getToken(provider, token string) error {
	if err := p.redisClient.SIsMember(provider+":tokens", token).Err(); err != nil {
		log.Printf("error in getting Token: %v", err)
		return err
	}
	return nil
}

func (p *paymentTokens) pushMessage(content string, pushID ...string) {
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

//addTrans adds a billerForm to a biller's SET transactions. We prefix biller id with `:trans`
func (p *paymentTokens) addTrans(id string, tran *billerForm) error {
	if _, err := p.redisClient.Ping().Result(); err != nil {
		return err
	}
	if _, err := p.redisClient.SAdd(id+":trans", tran).Result(); err != nil {
		return err
	}
	return nil
}

//billerExists checks if a biller id is already added in noebs billers
func (p *paymentTokens) billerExists(id string) bool {
	res, err := p.redisClient.SIsMember("noebs:billers", id).Result()
	if err != nil {
		log.Printf("error in sismember: %v", err)
		return res
	}
	return res
}

func (p *paymentTokens) FromMobile(id string, m ebs_fields.Merchant) error {
	log.Printf("the id is: %v", id)
	if ok := p.billerExists(id); ok { // ok means it does exist, we don't want exist ones
		return errors.New("biller_exists")
	}
	if err := p.newBiller(id); err != nil {
		return err
	}
	if _, err := p.StoreDeviceID(m); err != nil {
		return err
	}
	if err := p.SetPush(m, id); err != nil {
		return err
	}

	return nil
}

//StoreDeviceID is used to authorize merchant via device push ID
func (p *paymentTokens) StoreDeviceID(m ebs_fields.Merchant) (string, error) {
	// Encode me!
	data, err := json.Marshal(&m)
	if err != nil {
		return "", err
	}

	if err := p.redisClient.HSet("billers:auth", m.PushID, data).Err(); err != nil {
		return "", err
	}
	return m.PushID, nil

}

//GetAuthorization get merchant info through push id (for mobile only now)
func (p *paymentTokens) GetAuthorization(pushID string) (ebs_fields.Merchant, error) {

	var merchant ebs_fields.Merchant

	if res, err := p.redisClient.HGet("billers:auth", pushID).Result(); err != nil {
		return merchant, err
	} else {
		if err := json.Unmarshal([]byte(res), &merchant); err != nil {
			return merchant, err
		}
		return merchant, nil
	}
}

func (p *paymentTokens) PushToBillers(pushID string) (string, error) {
	if res, err := p.redisClient.Get(pushID).Result(); err != nil {
		return "", err
	} else {
		return res, nil
	}
}

//SetPush assigns a device push ID to noebs:billers, maps between device id and payment token
func (p *paymentTokens) SetPush(m ebs_fields.Merchant, id string) error {
	if _, err := p.redisClient.Set(m.PushID, id, 0).Result(); err != nil {
		return err
	}
	return nil
}

//getTrans gets a transaction from billers `biller:trans` SET stored in redis
func (p *paymentTokens) getTrans(id string) ([]billerForm, error) {
	var data []billerForm
	var d billerForm

	if res, err := p.redisClient.SMembers(id + ":trans").Result(); err != nil {
		return nil, err
	} else {
		for _, v := range res {
			err := json.Unmarshal([]byte(v), &d)
			if err != nil {
				return nil, err
			}
			data = append(data, d)
		}
		return data, nil
	}

}

//cancelTransaction cancel a given transaction by its UUID
func (p *paymentTokens) cancelTransaction(uuid string) error {
	if err := p.redisClient.Del("key_" + uuid).Err(); err != nil {
		return err
	}
	return nil
}

//getByID get transaction info to a specific clientID by its UUID
func (p *paymentTokens) getByID(key, uuid, clientID string) (billerForm, error) {

	m, err := p.getTrans(key)
	// log.Printf("The data is: %#v", m)
	if err != nil {
		return billerForm{}, err
	}
	for _, v := range m {
		log.Printf("The data is: %#v - Token is: %v\n", v, uuid)
		if v.Token == uuid || v.ID == clientID {
			log.Printf("the v is: %v", v)
			return v, nil
		}
	}
	return billerForm{}, errors.New("no_transactions")
}

func (p *paymentTokens) fromRedis(id string) (string, error) {

	//fixme maybe provide the user to get key
	res, err := p.redisClient.HMGet(id, "id").Result()
	if err != nil || res == nil {
		return "", err
	}

	log.Printf("The response is: %v", res)
	if res[0] == nil {
		return "", errors.New("nil values")
	}

	p.ID = res[0].(string)
	return p.ID, nil
}

func (p *paymentTokens) GetCashOut(namespace string) (cashoutFields, error) {

	var bill cashoutFields

	if !p.nsExists(namespace) {
		return bill, errors.New("not_found")
	}
	data, err := p.redisClient.HGet("cashout:billers:"+namespace, "info").Result()
	if err != nil {
		return bill, err
	}
	json.Unmarshal([]byte(data), &bill)

	return bill, nil

}

//UpdateCashOut implements read / write over cash out data
func (p *paymentTokens) UpdateCashOut(namespace string, f cashoutFields) error {

	var bill cashoutFields

	log.Printf("namespace is: %v", namespace)
	data, err := p.redisClient.HGet("cashout:billers:"+namespace, "info").Result()
	if err != nil {
		log.Printf("error in getting previous results: %v", err)
		return err
	}
	if err := json.Unmarshal([]byte(data), &bill); err != nil {
		return err
	}

	if f.Pan != "" {
		bill.Pan = f.Pan
	}
	if f.ExpDate != "" {
		bill.ExpDate = f.ExpDate
	}
	if f.Ipin != "" {
		bill.Ipin = f.Ipin
	}
	if f.Endpoint != "" {
		bill.Endpoint = f.Endpoint
	}

	res, err := json.Marshal(&bill)
	if err != nil {
		log.Printf("error in remarshaling: %v", err)
		return err
	}

	if _, err := p.redisClient.HSet("cashout:billers:"+namespace, "info", res).Result(); err != nil {
		log.Printf("error in storing data: %v", err)
		return err
	}
	return nil

}

func (p *paymentTokens) AddCashOut(namespace string, data cashoutFields) error {
	if p.nsExists(namespace) {
		return errors.New("item_exists")
	} else {
		p.redisClient.SAdd("noebs:cashouts", namespace)
		d, _ := json.Marshal(&data)
		if _, err := p.redisClient.HSet("cashout:billers:"+namespace, "info", d).Result(); err != nil {
			log.Printf("Error in storing hash: %v", err)
			return err
		}
		return nil
	}
}

func (p *paymentTokens) nsExists(ns string) bool {
	if found, _ := p.redisClient.SIsMember("noebs:cashouts", ns).Result(); found {
		return true
	}
	return false
}

func (p *paymentTokens) cshExists(ns, token string) bool {
	if found, _ := p.redisClient.SIsMember("cashout:"+ns, token).Result(); found {
		return true
	}
	return false
}

func (p *paymentTokens) addCsh(ns, token string) error {
	if _, err := p.redisClient.SAdd("cashout:"+ns, token).Result(); err != nil {
		return err
	}
	return nil
}

func (p *paymentTokens) markDone(ns, id string) error {
	if _, err := p.redisClient.SAdd("tasks:"+ns, id).Result(); err != nil {
		return err
	}
	return nil
}

func (p *paymentTokens) isDone(ns, id string) bool {
	if found, _ := p.redisClient.SIsMember("tasks:"+ns, id).Result(); found {
		return true
	}
	return false
}

//getAmount associated with cashout claim
func (p *paymentTokens) getAmount(ns, token string) (int, error) {

	if d, err := p.redisClient.HGet("cashouts:amounts"+ns, token).Result(); err != nil {
		return 0, err
	} else {
		amount, _ := strconv.ParseInt(d, 10, 0)
		return int(amount), nil
	}

}

//getAmount associated with cashout claim
func (p *paymentTokens) setAmount(ns, token string, amount int) error {
	if _, err := p.redisClient.HSet("cashouts:amounts"+ns, token, amount).Result(); err != nil {
		return err
	} else {
		return nil
	}
}

func (p *paymentTokens) setCash(ns, key string, card ebs_fields.CardsRedis) error {
	id, _ := json.Marshal(&card)
	if _, err := p.redisClient.HSet("cashouts:"+ns, key, id).Result(); err != nil {
		return err
	}
	return nil
}

func (p *paymentTokens) getCash(ns, key string) (ebs_fields.CardsRedis, error) {
	var cards ebs_fields.CardsRedis
	if d, err := p.redisClient.HGet("cashouts:"+ns, key).Result(); err != nil {
		return cards, err
	} else {
		json.Unmarshal([]byte(d), &cards)
		return cards, err
	}

}

func (p *paymentTokens) verifyCash() bool {
	if p.Amount == 0 || p.Name == "" {
		return false
	}
	return true
}

//NewCashout generates a new cashout item in noebs
func (p *paymentTokens) NewCashout(namespace string) (string, error) {

	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	if err := p.addCsh(namespace, id.String()); err != nil {
		return "", err
	}
	return id.String(), nil
}

//NewToken assigns a new token to an existing namespace (merchant, or biller id). It should fail for non-existing
// ids. For now it defaults to Sahil-wallet for sahil's specific transactions
// ID holds: Merchant name, mobile number, urls, and other info
//
func (p *paymentTokens) NewToken(namespace string) error {
	if err := p.new(); err != nil {
		return err
	}

	if _, err := p.storeKey(); err != nil {
		return err
	}
	if err := p.toRedis(); err != nil {
		return err
	}
	if err := p.id2owner(namespace); err != nil {
		return err
	}
	return nil
}

func (p *paymentTokens) newFromToken(id string) {
	p.ID = id
}

//ValidateToken only allow valid keys and valid transactions to be passed
func (p *paymentTokens) ValidateToken(id string, provider string) (bool, error) {

	//TODO(adonese): refactor this to a new function

	if ok, err := p.redisClient.SIsMember(SPECIAL_BILLERS, provider).Result(); !ok {
		log.Printf("item not found: %v", err)
		return false, err
	}
	p.newFromToken(id)

	if _, err := p.getKey(id); err != nil {

		log.Printf("error in getting key: %v - id is: %v", err, id)
		return false, err
	}

	return true, nil
}

//GetToken id is the token from url query params
func (p *paymentTokens) GetToken(id string) (bool, error) {

	p.newFromToken(id)

	if _, err := p.getKey(id); err != nil {
		return false, err
	}

	return true, nil
}

func (p *paymentTokens) invalidate(id string) error {
	if _, err := p.redisClient.Del("key_" + id).Result(); err != nil {
		return err
	}
	return nil
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
	Message string                              `json:"message,omitempty"`
	Code    int                                 `json:"code,omitempty"`
	Status  string                              `json:"status,omitempty"`
	Details ebs_fields.GenericEBSResponseFields `json:"details,omitempty"`
}

func newFromBytes(d []byte, code int) (response, error) {
	// TODO add handler for 400 errors

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
