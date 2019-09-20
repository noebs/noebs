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
	var id = 1
	for _, v := range cards {
		json.Unmarshal([]byte(v), &cb)
		cb.ID = id
		cardBytes = append(cardBytes, cb)
		id++
	}
	return cardBytes
}
