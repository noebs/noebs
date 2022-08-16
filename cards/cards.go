//Package cards implement tokenization for payment cards. Used internally by our other
// services to securely use our endpoints in variety of applications.
// The goal is to implement a Stripe-like tokenization system.

package cards

import (
	"encoding/json"
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/tokenization/tokenization"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis/v7"
)

type Service struct {
	Redis *redis.Client
}

//AddMobile adds a new mobile number entry to this current authorized user
func (s *Service) AddMobile(c *gin.Context) {

	var fields ebs_fields.MobileRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("mobile")
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
