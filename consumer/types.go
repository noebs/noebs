package consumer

import (
	"encoding/json"
	"github.com/adonese/noebs/ebs_fields"
	"regexp"
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
