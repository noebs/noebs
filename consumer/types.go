package consumer

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
)

const (
	SPECIAL_BILLERS = "special_billers"
)

type specialPaymentQueries struct {
	ID     string `form:"id,omitempty" binding:"required"`    //biller specific ids
	Token  string `form:"token,omitempty" binding:"required"` //noebs payment token
	IsJSON bool   `form:"json,omitempty"`
	To     string `form:"to,default=https://sahil2.soluspay.net"`
}

type billerForm struct {
	EBS          ebs_fields.GenericEBSResponseFields `json:"ebs_response"`
	ID           string                              `json:"id"`
	IsSuccessful bool                                `json:"is_successful"`
	Token        string                              `json:"payment_token"`
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
func (p *paymentTokens) getKey() (string, error) {

	if err := p.redisClient.Get("key_" + p.ID).Err(); err != nil {
		return "", err
	}
	return p.ID, nil

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
func (p *paymentTokens) exists(namespace string) bool {

	if _, err := p.redisClient.SMembers(namespace).Result(); err != nil {
		log.Printf("Error in exists: %v", err)
		return false
	}
	return true
}

//newBiller adds a new biller's namespace to our system
func (p *paymentTokens) newBiller(namespace string) error {
	if _, err := p.redisClient.SAdd(SPECIAL_BILLERS + namespace).Result(); err != nil {
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

//addTrans adds a billerForm to a biller's SET transactions. We prefix biller id with `:trans`
func (p *paymentTokens) addTrans(id string, tran *billerForm) error {
	if _, err := p.redisClient.Ping().Result(); err != nil {
		panic(err)
	}
	if _, err := p.redisClient.SAdd(id+":trans", tran).Result(); err != nil {
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

func (p *paymentTokens) GetToken(id string) (bool, error) {
	p.newFromToken(id)

	if _, err := p.getKey(); err != nil {
		return false, errors.New("invalid_token")
	}

	return true, nil
}

func (p *paymentTokens) invalidate(id string) error {
	if _, err := p.redisClient.Del(id).Result(); err != nil {
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
