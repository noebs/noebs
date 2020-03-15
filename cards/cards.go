package main

import (
	"encoding/json"
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis/v7"
	"github.com/sirupsen/logrus"
)

//GetCards returns a list of cards (default and others) associated to this
//authorized user
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
func AddCards(c *gin.Context) {
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
			z := &redis.Z{
				Member: buf,
			}
			if fields.IsMain {
				// refactor me, please!
				redisClient.HSet(username, "main_card", buf)

				redisClient.ZAdd(username+":cards", z)
			} else {
				redisClient.ZAdd(username+":cards", z)
			}
			c.JSON(http.StatusCreated, gin.H{"username": username, "cards": fields})
		}
	}

}

// EditCard a work in progress. This function needs to be reviewed and refactored
func EditCard(c *gin.Context) {
	redisClient := utils.GetRedis()

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
			keys, _ := redisClient.ZRange(username+":cards", int64(id), int64(id)).Result()

			// after getting the key, we are offloading it to the card instance
			cards := utils.RedisHelper(keys)
			z := &redis.Z{
				Member: buf,
			}
			if fields.IsMain {
				// refactor me, please!
				redisClient.HSet(username, "main_card", buf)
				// get the old item using the ID

				redisClient.ZRem(username+":cards", cards)
				redisClient.ZAdd(username+":cards", z)
			} else {
				redisClient.ZRem(username+":cards", cards)
				redisClient.ZAdd(username+":cards", z)
			}

			c.JSON(http.StatusNoContent, gin.H{"username": username, "cards": buf, "cards_old": cards})
		}
	}

}

// RemoveCard a work in progress. This function needs to be reviewed and refactored
func RemoveCard(c *gin.Context) {
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

			if fields.IsMain {
				redisClient.HDel(username+":cards", "main_card")
			} else {
				_, err := redisClient.ZRem(username+":cards", buf).Result()
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "unable_to_delete"})
				}
			}

			c.JSON(http.StatusNoContent, gin.H{"username": username, "cards": buf})
		}
	}

}

//AddMobile adds a new mobile number entry to this current authorized user
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

//GetMobile returns a user list of mobile numbers from redis database
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
