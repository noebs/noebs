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

func GetMainEngine() *gin.Engine {
	route := gin.Default()

	route.HandleMethodNotAllowed = true

	route.POST("/login", LoginHandler)
	// This is like isAlive one...

	route.POST("/create", CreateUser)

	route.POST("/get_service", GetServiceID)

	route.POST("/test", func(context *gin.Context) {
		context.JSON(http.StatusOK, gin.H{"message": true, "code": "ok"})
	})
	auth := route.Group("/admin", AuthMiddleware())

	auth.POST("/test", func(context *gin.Context) {
		context.JSON(http.StatusOK, gin.H{"message": true, "code": "ok"})
	})

	return route
}

func main() {
	r := GetMainEngine()
	r.Run(":8001")
}

var jwtKey = keyFromEnv()

func LoginHandler(c *gin.Context) {

	var req UserLogin
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
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

	if notFound := db.Preload("JWT").Where("username = ?", req.Username).First(&u).RecordNotFound(); notFound {
		// service id is not found
		log.Printf("User with service_id %s is not found.", req.Username)
		c.JSON(http.StatusBadRequest, gin.H{"message": notFound, "code": "not_found"})
		return
	}

	redisClient := utils.GetRedis()
	res, err := redisClient.Get(req.Username + ":login_counts").Result()

	if err == redis.Nil {
		// the has just logged in
		redisClient.Set(req.Username+":login_counts", 0, time.Hour)
	} else if err == nil {
		count, _ := strconv.Atoi(res)
		if count >= 5 {
			// Allow users to use another login method (e.g., totp, or they should reset their password)
			c.JSON(http.StatusBadRequest, gin.H{"message": "Too many wrong login attempts", "code": "maximum_login"})
			return
		}
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)); err != nil {
		log.Printf("there is an error in the password %v", err)
		redisClient.Incr(req.Username + ":login_counts")
		c.JSON(http.StatusBadRequest, gin.H{"message": "wrong password entered", "code": "wrong_password"})

		return
	}
	token, err := GenerateJWT(u.Username, jwtKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err)
		return
	}

	u.JWT.SecretKey = string(jwtKey)
	u.JWT.CreatedAt = time.Now().UTC()

	err = db.Save(&u).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}
	c.Writer.Header().Set("Authorization", token)

	c.JSON(http.StatusOK, gin.H{"authorization": token, "user": u})

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

	if err := db.Create(&u).Error; err != nil {
		// unable to create this user; see possible reasons

		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "duplicate_username"})
		return
	}
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
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Token has expired", "code": "jwt_expired"})
				return
			} else {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Malformed token", "code": "jwt_malformed"})
				return
			}
		} else if err == nil {
			c.Set("username", claims.Username)
			log.Printf("the username is: %s", claims.Username)
			c.Next()
		}
	}

}

func GenerateSecretKey(n int) ([]byte, error) {
	key := make([]byte, n)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// keyFromEnv either generates or retrieve a JWT which will be used to generate a secret key
func keyFromEnv() []byte {
	// it either checks for environment for the specific key, or generates and saves a one
	if key := os.Getenv("Jwt-Token"); key != "" {
		return []byte(key)
	}
	key, _ := GenerateSecretKey(50)
	os.Setenv("Jwt-Token", string(key))
	return key
}
