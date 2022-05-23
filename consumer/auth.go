package consumer

import (
	"fmt"
	"net/http"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis/v7"
	"github.com/golang-jwt/jwt"
	"github.com/jinzhu/gorm"
	"golang.org/x/crypto/bcrypt"
)

type State struct {
	Db        *gorm.DB
	Redis     *redis.Client
	Auth      Auther
	UserModel gateway.UserModel
	UserLogin gateway.UserLogin
}

type Auther interface {
	VerifyJWT(token string) (*gateway.TokenClaims, error)
	GenerateJWT(token string) (string, error)
}

//GenerateAPIKey An Admin-only endpoint that is used to generate api key for our clients
// the user must submit their email to generate a unique token per email.
// FIXME #59 #58 #61 api generation should be decoupled from apigateway package
func (s *State) GenerateAPIKey(c *gin.Context) {
	var m map[string]string
	if err := c.ShouldBindJSON(&m); err != nil {
		if _, ok := m["email"]; ok {
			k, _ := gateway.GenerateAPIKey()
			s.Redis.SAdd("apikeys", k)
			c.JSON(http.StatusOK, gin.H{"result": k})
			return
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"message": "missing_field"})
			return
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"message": "error in email"})

	}

}

//ApiKeyMiddleware used to authenticate clients using X-Email and X-API-Key headers
//FIXME issue #58 #61
func (s *State) ApiKeyMiddleware(c *gin.Context) {
	email := c.GetHeader("X-Email")
	key := c.GetHeader("X-API-Key")
	if email == "" || key == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "unauthorized"})
		return
	}

	res, err := s.Redis.HGet("api_keys", email).Result()
	if err != redis.Nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "unauthorized"})
		return
	}
	if key == res {
		c.Next()
	} else {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "unauthorized"})
		return
	}
}

//FIXME issue #58 #61
func (s *State) IpFilterMiddleware(c *gin.Context) {
	ip := c.ClientIP()
	if u := c.GetString("username"); u != "" {
		s.Redis.HIncrBy(u+":ips_count", ip, 1)
		c.Next()
	} else {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "unauthorized_access"})
	}
}

//FIXME #60 make LoginHandler in consumer apis #61
//FIXME(adonese): #160 make login flow simpler. The code is rubbish
func (s *State) LoginHandler(c *gin.Context) {

	req := s.UserLogin

	if err := c.ShouldBindWith(&req, binding.JSON); err != nil {
		// The request is wrong
		log.Printf("The request is wrong. %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "bad_request"})
		return
	}

	s.Db.AutoMigrate(&gateway.Service{}, &gateway.UserModel{}, &gateway.UserLogin{})

	log.Printf("the processed request is: %v\n", req)
	u := s.UserModel

	if notFound := s.Db.Preload("jwt").Where("username = ? or email = ? or mobile = ?", strings.ToLower(req.Username), strings.ToLower(req.Username), strings.ToLower(req.Username)).First(&u).RecordNotFound(); notFound {
		// service id is not found
		log.Printf("User with service_id %s is not found.", req.Username)
		c.JSON(http.StatusBadRequest, gin.H{"message": notFound, "code": "not_found"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "wrong password entered", "code": "wrong_password"})
		return
	}

	token, err := s.Auth.GenerateJWT(u.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}
	c.Writer.Header().Set("Authorization", token)
	c.JSON(http.StatusOK, gin.H{"authorization": token, "user": u})
}

//FIXME #61 refactor some of these apis for consumer services only
func (s *State) RefreshHandler(c *gin.Context) {

	// just handle the simplest case, authorization is not provided.
	h := c.GetHeader("Authorization")
	if h == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "empty header was sent", "code": "unauthorized"})
		return
	}

	claims, err := s.Auth.VerifyJWT(h)
	if e, ok := err.(*jwt.ValidationError); ok {
		if e.Errors&jwt.ValidationErrorExpired != 0 {
			log.Printf("the username is: %s", claims.Username)

			auth, _ := s.Auth.GenerateJWT(claims.Username)
			c.Writer.Header().Set("Authorization", auth)
			c.JSON(http.StatusOK, gin.H{"authorization": auth})

		} else {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Malformed token", "code": "jwt_malformed"})
			return
		}
	} else if err == nil {
		// FIXME it is better to let the endpoint explicitly Get the claim off the user
		//  as we will assume the auth server will reside in a different domain!
		log.Printf("the username is: %s", claims.Username)

		auth, _ := s.Auth.GenerateJWT(claims.Username)
		c.Writer.Header().Set("Authorization", auth)
		c.JSON(http.StatusOK, gin.H{"authorization": auth})
	}
}

//FIXME issue #61
func (s *State) LogOut(c *gin.Context) {
	//TODO implement logout API to limit the number of currently logged in devices
	// just handle the simplest case, authorization is not provided.
	h := c.GetHeader("Authorization")
	if h == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "empty header was sent",
			"code": "unauthorized"})
		return
	}

	claims, err := s.Auth.VerifyJWT(h)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": err.Error(), "code": "malformed_jwt_token"})
		return
	}

	username := claims.Username
	if username != "" {
		s.Redis.Decr(username + ":logged_in_devices")
		c.JSON(http.StatusNoContent, gin.H{"message": "Device Successfully Logged Out"})
		return
	} else {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "Unauthorized", "code": "unauthorized"})
		return
	}
}

//FIXME issue #61
func (s *State) CreateUser(c *gin.Context) {
	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": serverError.Error()})
		return
	}

	defer db.Close()

	u := s.UserModel
	if err := s.Db.AutoMigrate(&u).Error; err != nil {
		// log the error, but don't quit.
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	err = c.ShouldBindBodyWith(&u, binding.JSON)
	// make the errors insane
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	// validate u.Password to include at least one capital letter, one symbol and one number
	// and that it is at least 8 characters long
	if !validatePassword(u.Password) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Password must be at least 8 characters long, and must include at least one capital letter, one symbol and one number", "code": "password_invalid"})
		return
	}

	// make sure that the user doesn't exist in the database

	err = u.HashPassword()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

	// make the user capital - small
	u.SanitizeName()
	if err := db.Create(&u).Error; err != nil {
		// unable to create this user; see possible reasons
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "duplicate_username"})
		return
	}

	s.Redis.Set(u.Mobile, u.Username, 0)
	ip := c.ClientIP()
	s.Redis.HSet(u.Username+":ips_count", "first_ip", ip)

	c.JSON(http.StatusCreated, gin.H{"ok": "object was successfully created", "details": u})
}

//FIXME issue #61
func GetServiceID(c *gin.Context) {
	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
	}
	defer db.Close()

	db.AutoMigrate(&Service{})

	id := c.Query("id")
	if id == "" {
		c.AbortWithStatusJSON(400, gin.H{"message": errNoServiceID.Error()})
	}

	fmt.Printf("the qparam is: %v\n", id)
	var res Service

	if err := db.Where("username = ?", id).First(&res).Error; err != nil {
		c.AbortWithStatusJSON(404, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": "this object is available"})
}

//APIAuth API-Key middleware. Currently is used by consumer services
//FIXME issue #61
func (s *State) APIAuth() gin.HandlerFunc {

	return func(c *gin.Context) {
		if key := c.GetHeader("api-key"); key != "" {
			if !isMember("apikeys", key, s.Redis) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": "wrong_api_key",
					"message": "visit https://soluspay.net/contact for a key"})
				return
			}
		}
		c.Next()
	}

}
