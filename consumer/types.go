package consumer

import (
	"encoding/json"
	"regexp"
	"strconv"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/google/uuid"
)

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

type paymentTokens struct {
	Name   string  `json:"name,omitempty" validator:"required"`
	Amount float32 `json:"amount,omitempty" validator:"required"`
	ID     string  `json:"id,omitempty"`
	UUID   string  `json:"uuid"`
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

func (p *paymentTokens) toRedis() error {
	r := utils.GetRedis()

	id := p.getUUID()
	h := p.toMap()

	if _, err := r.HMSet(id, h).Result(); err != nil {
		return err
	}
	return nil

}

func (p *paymentTokens) getFromRedis(id string) error {
	r := utils.GetRedis()
	res, err := r.HMGet(id, "id", "amount").Result()
	if err != nil {
		return err
	}
	p.ID = res[0].(string)
	amount, _ := strconv.ParseFloat(res[1].(string), 32)
	p.Amount = float32(amount)
	return nil
}

func (p *paymentTokens) invalidate(id string) error {
	r := utils.GetRedis()
	_, err := r.Del(id).Result()
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
