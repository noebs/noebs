package gateway

import (
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/adonese/noebs/utils"
	"github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis"
	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"os"
	"strconv"
	"time"
)

var apiKey = make([]byte, 16)
var jwtKey = keyFromEnv()

func GenerateAPIKey(c *gin.Context) {
	m := make(map[string]string)
	if err := c.ShouldBindJSON(m); err != nil {
		if email, ok := m["email"]; ok {
			k, _ := generateApiKey()
			getRedis := utils.GetRedis()
			getRedis.HSet("api_keys", email, k)
			c.JSON(http.StatusOK, gin.H{"result": k})
			return
		}
	}
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "error in email"})
}

func ApiKeyMiddleware(c *gin.Context) {
	email := c.GetHeader("X-Email")
	key := c.GetHeader("X-API-Key")
	if email == "" || key == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "unauthorized"})
		return
	}
	redisClient := utils.GetRedis()
	res, err := redisClient.HGet("api_keys", email).Result()
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
func IpFilterMiddleware(c *gin.Context) {
	ip := c.ClientIP()
	if u := c.GetString("username"); u != "" {
		redisClient := utils.GetRedis()
		redisClient.HIncrBy(u+":ips_count", ip, 1)
		c.Next()
	} else {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "unauthorized_access"})
	}
}

func LoginHandler(c *gin.Context) {

	var req UserLogin
	if err := c.ShouldBindWith(&req, binding.JSON); err != nil {
		// The request is wrong
		log.Printf("The request is wrong. %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "bad_request"})
		return
	}

	//db connection. Not good.
	db, err := gorm.Open("sqlite3", "test.db")

	if err != nil {
		log.Fatalf("There's an error in DB connection, %v", err)
	}

	defer db.Close()

	// do the Models migrations here. The ones you will be using
	db.AutoMigrate(&Service{}, &JWT{}, &UserModel{}, &UserLogin{})

	log.Printf("the processed request is: %v\n", req)
	var u UserModel

	if notFound := db.Preload("jwt").Where("username = ?", req.Username).First(&u).RecordNotFound(); notFound {
		// service id is not found
		log.Printf("User with service_id %s is not found.", req.Username)
		c.JSON(http.StatusBadRequest, gin.H{"message": notFound, "code": "not_found"})
		return
	}

	// Make sure the user doesn't have any active sessions!
	redisClient := utils.GetRedis()
	lCount, err := redisClient.Get(req.Username + ":logged_in_devices").Result()

	num, _ := strconv.Atoi(lCount)
	// Allow for the user to be logged in -- add allowance through someway
	if err != redis.Nil && num > 1 {
		// The user is already logged in somewhere else. Communicate that to them, clearly!
		//c.JSON(http.StatusBadRequest, gin.H{"code": "user_logged_elsewhere",
		//	"error": "You are logging from another device. You can only have one valid session"})
		//return
		log.Print("The user is logging from a different location")
	}

	// make sure number of failed logged_in counts doesn't exceed the allowed threshold.
	res, err := redisClient.Get(req.Username + ":login_counts").Result()
	if err == redis.Nil {
		// the has just logged in
		redisClient.Set(req.Username+":login_counts", 0, time.Hour)
	} else if err == nil {
		count, _ := strconv.Atoi(res)
		if count >= 5 {
			// Allow users to use another login method (e.g., totp, or they should reset their password)
			// Lock their account
			//redisClient.HSet(req.Username, "suspecious_behavior", 1)
			redisClient.HIncrBy(req.Username, "suspicious_behavior", 1)
			ttl, _ := redisClient.TTL(req.Username + ":login_counts").Result()
			c.JSON(http.StatusBadRequest, gin.H{"message": "Too many wrong login attempts",
				"code": "maximum_login", "ttl_minutes": ttl.Minutes()})
			return
		}
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)); err != nil {
		log.Printf("there is an error in the password %v", err)
		redisClient.Incr(req.Username + ":login_counts")
		c.JSON(http.StatusBadRequest, gin.H{"message": "wrong password entered", "code": "wrong_password"})
		return
	}
	// it is a successful login attempt
	redisClient.Del(req.Username + ":login_counts")
	token, err := GenerateJWT(u.Username, jwtKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	u.jwt.SecretKey = string(jwtKey)
	u.jwt.CreatedAt = time.Now().UTC()

	err = db.Save(&u).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	c.Writer.Header().Set("Authorization", token)

	// Redis add The user is now logged in -- and has active session
	redisClient.Incr(req.Username + ":logged_in_devices")

	c.JSON(http.StatusOK, gin.H{"authorization": token, "user": u})

}

func RefreshHandler(c *gin.Context) {

	// just handle the simplest case, authorization is not provided.
	h := c.GetHeader("Authorization")
	if h == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "empty header was sent",
			"code": "unauthorized"})
		return

	}

	claims, err := VerifyJWT(h, jwtKey)
	if e, ok := err.(*jwt.ValidationError); ok {
		if e.Errors&jwt.ValidationErrorExpired != 0 {
			// Generate a new token
		} else {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Malformed token", "code": "jwt_malformed"})
			return
		}
	}
	if claims == nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Malformed token", "code": "jwt_malformed"})
		return
	}
	secret, _ := GenerateSecretKey(50)
	token, _ := GenerateJWT(claims.Username, secret)
	c.Writer.Header().Set("Authorization", token)

	c.JSON(http.StatusOK, gin.H{"authorization": token})
}

func LogOut(c *gin.Context) {
	//TODO implement logout API to limit the number of currently logged in devices
	// just handle the simplest case, authorization is not provided.
	h := c.GetHeader("Authorization")
	if h == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "empty header was sent",
			"code": "unauthorized"})
		return
	}

	claims, err := VerifyJWT(h, jwtKey)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": err.Error(), "code": "malformed_jwt_token"})
		return
	}

	username := claims.Username
	if username != "" {
		redisClient := utils.GetRedis()
		redisClient.Decr(username + ":logged_in_devices")
		c.JSON(http.StatusNoContent, gin.H{"message": "Device Successfully Logged Out"})
		return
	} else {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "Unauthorized", "code": "unauthorized"})
		return
	}
}

func CreateUser(c *gin.Context) {
	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": serverError.Error()})
		return
	}

	defer db.Close()

	var u UserModel
	if err := db.AutoMigrate(&UserModel{}).Error; err != nil {
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

	// make sure that the user doesn't exist in the database

	err = u.hashPassword()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

	// make the user capital - small
	u.sanitizeName()
	if err := db.Create(&u).Error; err != nil {
		// unable to create this user; see possible reasons
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "duplicate_username"})
		return
	}
	redisClient := utils.GetRedis()
	redisClient.Set(u.Mobile, u.Username, 0)
	ip := c.ClientIP()
	redisClient.HSet(u.Username+":ips_count", "first_ip", ip)

	c.JSON(http.StatusCreated, gin.H{"ok": "object was successfully created", "details": u})
}

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

var (
	serverError       = errors.New("unable to connect to the DB")
	ErrCreateDbRow    = errors.New("unable to create a new db row/column")
	errNoServiceID    = errors.New("empty Service ID was entered")
	errObjectNotFound = errors.New("object not found")
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// just handle the simplest case, authorization is not provided.
		h := c.GetHeader("Authorization")
		if h == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "empty header was sent",
				"code": "unauthorized"})
			return
		}
		claims, err := VerifyJWT(h, jwtKey)
		if e, ok := err.(*jwt.ValidationError); ok {
			if e.Errors&jwt.ValidationErrorExpired != 0 {
				// in this case you might need to give it another spin
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Token has expired", "code": "jwt_expired"})
				return
			} else {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Malformed token", "code": "jwt_malformed"})
				return
			}
		} else if err == nil {
			// FIXME it is better to let the endpoint explicitly Get the claim off the user
			//  as we will assume the auth server will reside in a different domain!

			c.Set("username", claims.Username)
			log.Printf("the username is: %s", claims.Username)
			c.Next()
		}
	}

}

func ApiAuth() gin.HandlerFunc {
	r := utils.GetRedis()
	return func(c *gin.Context) {
		if key := c.GetHeader("api-key"); key != "" {
			if ok := isMember("api_keys", key, r); ok {
				c.Next()
			}
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": "wrong_api_key",
			"message": "visit https://soluspay.net/contact for a key"})
	}
}
func GenerateSecretKey(n int) ([]byte, error) {
	key := make([]byte, n)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// keyFromEnv either generates or retrieve a jwt which will be used to generate a secret key
func keyFromEnv() []byte {
	// it either checks for environment for the specific key, or generates and saves a one
	if key := os.Getenv("NOEBS_JWT_TOKEN"); key != "" {
		return []byte(key)
	}
	key, _ := GenerateSecretKey(50)
	os.Setenv("NOEBS_JWT_TOKEN", string(key))
	return key
}

func OptionsMiddleware(c *gin.Context) {
	if c.Request.Method != "OPTIONS" {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Next()
	} else {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "authorization, origin, content-type, accept")
		c.Header("Allow", "HEAD,GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Content-Type", "application/json")
		c.AbortWithStatus(http.StatusOK)
	}
}

func generateApiKey() (string, error) {
	_, err := rand.Read(apiKey)
	a := fmt.Sprintf("%x", apiKey)
	return a, err
}

func isMember(key, val string, r *redis.Client) bool {
	if ok, err := r.SIsMember(key, val).Result(); err == nil && ok {
		if ok {
			return true
		}
	}
	return false
}
