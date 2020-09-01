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

type billerForm struct {
	EBS          ebs_fields.GenericEBSResponseFields `json:"ebs_response"`
	ID           string                              `json:"id"`
	IsSuccessful bool                                `json:"is_successful"`
	Token        string                              `json:"payment_token"`
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

func (p *paymentTokens) storeKey() (string, error) {

	// tt := 30 * time.Minute
	tt := 30 * time.Minute
	log.Printf("the key we are storing is: %v", p.ID)

	if err := p.redisClient.Set("key_"+p.ID, p.ID, tt).Err(); err != nil {

		return "", err
	}
	return p.ID, nil

}

func (p *paymentTokens) getKey() (string, error) {

	if err := p.redisClient.Get("key_" + p.ID).Err(); err != nil {
		return "", err
	}
	return p.ID, nil

}

func (p *paymentTokens) toRedis() error {

	h := p.toMap()

	if _, err := p.redisClient.HMSet(p.ID, h).Result(); err != nil {

		return err
	}
	return nil

}

func (p *paymentTokens) id2owner() error {

	h := p.toMap()

	if _, err := p.redisClient.SAdd("sahil_wallet", h).Result(); err != nil {

		return err
	}
	return nil

}

func (p *paymentTokens) addTrans(id string, tran []byte) error {
	if _, err := p.redisClient.Ping().Result(); err != nil {
		panic(err)
	}
	if _, err := p.redisClient.SAdd(id+":trans", tran).Result(); err != nil {
		return err
	}
	return nil
}

func (p *paymentTokens) getTrans(id string) ([]ebs_fields.GenericEBSResponseFields, error) {
	var data []ebs_fields.GenericEBSResponseFields
	var d ebs_fields.GenericEBSResponseFields

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

func (p *paymentTokens) cancelTransaction(uuid string) error {
	if err := p.redisClient.Del("key_" + uuid).Err(); err != nil {
		return err
	}
	return nil
}

func (p *paymentTokens) getByID(uuid string) (ebs_fields.GenericEBSResponseFields, error) {

	m, err := p.getTrans(uuid)
	if err != nil {
		return ebs_fields.GenericEBSResponseFields{}, err
	}
	for _, v := range m {
		if v.UUID == uuid {
			return v, nil
		}
	}
	return ebs_fields.GenericEBSResponseFields{}, errors.New("not_found")
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

func (p *paymentTokens) NewToken() error {
	if err := p.new(); err != nil {
		return err
	}

	if _, err := p.storeKey(); err != nil {
		return err
	}
	if err := p.toRedis(); err != nil {
		return err
	}
	if err := p.id2owner(); err != nil {
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
	_, err := p.redisClient.Del(id).Result()
	if err != nil {
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
