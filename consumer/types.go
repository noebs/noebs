package consumer

import (
	"encoding/json"
	"github.com/adonese/noebs/ebs_fields"
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
