package consumer

import (
	"encoding/json"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis"
	"github.com/sirupsen/logrus"
	"net/http"
)

func CardFromNumber(c *gin.Context) {
	// the user must submit in their mobile number *ONLY*, and it is get
	if q, ok := c.GetQuery("mobile_number"); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"message": "mobile number is empty", "code": "empty_mobile_number"})
		return
	} else {
		// now search through redis for this mobile number!
		redisClient := utils.GetRedis()
		// first check if we have already collected that number before
		pan, err := redisClient.Get(q + ":pan").Result()
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"result": pan})
			return
		}
		username, err := redisClient.Get(q).Result()
		if err == redis.Nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
			return
		}
		if pan, ok := utils.PanfromMobile(username, redisClient); ok {
			c.JSON(http.StatusOK, gin.H{"result": pan})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
		}
	}

}

//GetCards Get all cards for the currently authorized user
func GetCards(c *gin.Context) {
	redisClient := utils.GetRedis()
	username := c.GetString("username")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
	} else {
		cards, err := redisClient.ZRange(username+":cards", 0, -1).Result()
		if err != nil {
			// handle the error somehow
			logrus.WithFields(logrus.Fields{
				"error":   "unable to get results from redis",
				"message": err.Error(),
			}).Info("unable to get results from redis")
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "error in redis"})
		}
		cardBytes := cardsFromZ(cards)
		m, _ := redisClient.HGet(username+":cards", "main_card").Result()
		mCard := cardsFromZ([]string{m})
		c.JSON(http.StatusOK, gin.H{"cards": cardBytes, "main_card": mCard[0]})
	}
}

//AddCards Allow users to add card to their profile
// if main_card was set to true, then it will be their main card AND
// it will remove the previously selected one FIXME
func AddCards(c *gin.Context) {
	redisClient := utils.GetRedis()
	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
		return
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			// make sure the length of the card and expDate is valid
			z := &redis.Z{
				Member: buf,
			}
			if fields.IsMain {
				// refactor me, please!
				ucard := card{"main_card": buf, "pan": fields.PAN, "exp_date": fields.Expdate}
				redisClient.HMSet(username, ucard)
				redisClient.ZAdd(username+":cards", z)
			} else {
				_, err := redisClient.ZAdd(username+":cards", z).Result()
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
					return
				}
				// also it is necessary to add it into a list of user's pans
				//FIXME better handle this error smh
				redisClient.RPush(username+":pans", fields.PAN)
			}
			c.JSON(http.StatusCreated, gin.H{"username": username, "cards": fields})
		}
	}
}

//EditCard allow authorized users to edit their cards (e.g., edit pan / expdate)
func EditCard(c *gin.Context) {
	redisClient := utils.GetRedis()

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
			return
		}
		// core functionality
		id := fields.ID
		{
			// step 1: removing the card; copied from RemoveCard
			if fields.IsMain {
				redisClient.HDel(username+":cards", "main_card")
			} else {
				_, err := redisClient.ZRemRangeByRank(username+":cards", int64(id-1), int64(id-1)).Result()
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "unable_to_delete"})
					return
				}
			}
		}

		// step 2: Add the card; copied from AddCard

		{
			z := &redis.Z{
				Member: buf,
			}
			if fields.IsMain {
				// refactor me, please!
				redisClient.HSet(username, "main_card", buf)

				redisClient.ZAdd(username+":cards", z)
			} else {
				_, err := redisClient.ZAdd(username+":cards", z).Result()
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
					return
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"username": username, "cards": fields})

	}

}

//RemoveCard allow authorized users to remove their card
// when the send the card id (from its list in app view)
func RemoveCard(c *gin.Context) {
	redisClient := utils.GetRedis()

	var fields ebs_fields.ItemID
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
		// there is no error in the request body
	} else {
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else if fields.ID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "card id not provided", "code": "card_id_not_provided"})
			return
		}
		// core functionality
		id := fields.ID

		if fields.IsMain {
			redisClient.HDel(username+":cards", "main_card")
		} else {
			_, err := redisClient.ZRemRangeByRank(username+":cards", int64(id-1), int64(id-1)).Result()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "unable_to_delete"})
				return
			}
		}

		c.JSON(http.StatusOK, gin.H{"username": username, "cards": fields})

	}

}

func AddMobile(c *gin.Context) {
	redisClient := utils.GetRedis()

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
				redisClient.HSet(username, "main_mobile", buf)
				redisClient.SAdd(username+":cards", buf)
			} else {
				redisClient.SAdd(username+":mobile_numbers", buf)
			}

			c.JSON(http.StatusCreated, gin.H{"username": username, "cards": string(buf)})
		}
	}

}

func GetMobile(c *gin.Context) {
	redisClient := utils.GetRedis()

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
				redisClient.HSet(username, "main_mobile", buf)
				redisClient.SAdd(username+":mobile_numbers", buf)
			} else {
				redisClient.SAdd(username+":mobile_numbers", buf)
			}

			c.JSON(http.StatusCreated, gin.H{"username": username, "mobile_numbers": string(buf)})
		}
	}

}
