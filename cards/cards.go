//Package cards implement tokenization for payment cards. Used internally by our other
// services to securely use our endpoints in variety of applications.
// The goal is to implement a Stripe-like tokenization system.

package cards

import (
	"encoding/json"
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/adonese/tokenization/tokenization"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis/v7"
	"github.com/sirupsen/logrus"
)

type Service struct {
	Redis *redis.Client
}

//GetCards returns a list of cards (default and others) associated to this
//authorized user
func (s *Service) GetCards(c *gin.Context) {

	username := c.GetString("username")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
	} else {
		cards, err := s.Redis.ZRange(username+":cards", 0, -1).Result()
		if err != nil {
			// handle the error somehow
			logrus.WithFields(logrus.Fields{
				"error":   "unable to get results from redis",
				"message": err.Error(),
			}).Info("unable to get results from redis")
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "error in redis"})
		}

		// unmrshall cards and send them back to the user
		// they should be a []Cards

		var cb ebs_fields.CardsRedis
		var cardBytes []ebs_fields.CardsRedis
		var id = 1
		for _, v := range cards {
			json.Unmarshal([]byte(v), &cb)
			cb.ID = id
			cardBytes = append(cardBytes, cb)
			id++
		}
		c.JSON(http.StatusOK, gin.H{"cards": cardBytes})
	}

}

//AddCards to the current authorized user
func (s *Service) AddCards(c *gin.Context) {

	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			z := &redis.Z{
				Member: buf,
			}
			if fields.IsMain {
				// refactor me, please!
				s.Redis.HSet(username, "main_card", buf)

				s.Redis.ZAdd(username+":cards", z)
			} else {
				s.Redis.ZAdd(username+":cards", z)
			}
			c.JSON(http.StatusCreated, gin.H{"username": username, "cards": fields})
		}
	}

}

// EditCard a work in progress. This function needs to be reviewed and refactored
func (s *Service) EditCard(c *gin.Context) {

	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusBadRequest, gin.H{"message": "unauthorized_access", "code": "empty_card_id"})
		} else if fields.ID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "card id not submitted", "code": "empty_card_id"})
		} else {
			//FIXME please
			id := fields.ID
			keys, _ := s.Redis.ZRange(username+":cards", int64(id), int64(id)).Result()

			// after getting the key, we are offloading it to the card instance
			cards := utils.RedisHelper(keys)
			z := &redis.Z{
				Member: buf,
			}
			if fields.IsMain {
				// refactor me, please!
				s.Redis.HSet(username, "main_card", buf)
				// get the old item using the ID

				s.Redis.ZRem(username+":cards", cards)
				s.Redis.ZAdd(username+":cards", z)
			} else {
				s.Redis.ZRem(username+":cards", cards)
				s.Redis.ZAdd(username+":cards", z)
			}

			c.JSON(http.StatusNoContent, gin.H{"username": username, "cards": buf, "cards_old": cards})
		}
	}

}

// RemoveCard a work in progress. This function needs to be reviewed and refactored
func (s *Service) RemoveCard(c *gin.Context) {

	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
		// there is no error in the request body
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else if fields.ID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "card id not provided", "code": "card_id_not_provided"})

			if fields.IsMain {
				s.Redis.HDel(username+":cards", "main_card")
			} else {
				_, err := s.Redis.ZRem(username+":cards", buf).Result()
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "unable_to_delete"})
				}
			}

			c.JSON(http.StatusNoContent, gin.H{"username": username, "cards": buf})
		}
	}

}

//AddMobile adds a new mobile number entry to this current authorized user
func (s *Service) AddMobile(c *gin.Context) {

	var fields ebs_fields.MobileRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			if fields.IsMain {
				s.Redis.HSet(username, "main_mobile", buf)
				s.Redis.SAdd(username+":cards", buf)
			} else {
				s.Redis.SAdd(username+":mobile_numbers", buf)
			}

			c.JSON(http.StatusCreated, gin.H{"username": username, "cards": string(buf)})
		}
	}

}

//GetMobile returns a user list of mobile numbers from redis database
func (s *Service) GetMobile(c *gin.Context) {

	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			if fields.IsMain {
				s.Redis.HSet(username, "main_mobile", buf)
				s.Redis.SAdd(username+":mobile_numbers", buf)
			} else {
				s.Redis.SAdd(username+":mobile_numbers", buf)
			}

			c.JSON(http.StatusCreated, gin.H{"username": username, "mobile_numbers": string(buf)})
		}
	}

}

//Tokenize returns a token representations of the Card
func (s *Service) Tokenize(c *gin.Context) {
	var card ebs_fields.TokenCard

	if err := c.ShouldBind(&card); err != nil {
		validation := ebs_fields.ValidationError{Code: "request_error", Message: "Request empty"}
		c.JSON(http.StatusOK, validation)
		return
	}
	token, err := tokenization.NewCard()
	if err != nil {
		validation := ebs_fields.ValidationError{Code: "tokenization_error", Message: err.Error()}
		c.JSON(http.StatusOK, validation)
		return
	}
	token.Pan = card.Pan
	token.Pin = card.Pin
	token.Expdate = card.Expdate

	if err := token.NewToken(); err != nil {
		validation := ebs_fields.ValidationError{Code: "tokenization_error", Message: err.Error()}
		c.JSON(http.StatusOK, validation)
		return
	}
	c.JSON(http.StatusOK, token.GetTokenized())
}
