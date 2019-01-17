package gateway

import (
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/jinzhu/gorm"
	"golang.org/x/crypto/bcrypt"
	"log"
	"math/rand"
	"net/http"
	"time"
)

func GetMainEngine() *gin.Engine {
	route := gin.Default()

	route.HandleMethodNotAllowed = true

	route.POST("/login", LoginHandler)
	// This is like isAlive one...
	route.POST("/test", func(context *gin.Context) {
		context.JSON(http.StatusOK, gin.H{"message": true, "code": "ok"})
	})
	return route
}

func LoginHandler(c *gin.Context) {
	// Get the request object, check it.
	// return back either
	// - a JWT token if successful
	// - or, an unauthorized
	// Unmarshall the JSON object onto this struct

	var requestStruct Request
	if err := c.ShouldBindBodyWith(&requestStruct, binding.JSON); err != nil {
		// The request is wrong
		log.Fatalf("The request is wrong. %v", err)
		c.AbortWithStatusJSON(400, gin.H{"message": "Bad Request", "code": "bad_request"})
	}

	// Now, the request is well valid. Let us check its credentials.

	//db connection. Not good.
	db, err := gorm.Open("sqlite3", "/tmp/gorm_db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	// do the Models migrations here. The ones you will be using
	db.AutoMigrate(&Service{}, &JWT{})

	var service Service

	// checke the entered password is correct.
	// if not: return 401
	// if yes, generate a JWT token.
	if notFound := db.Preload("JWT").Where("service_name = ?", requestStruct.ServiceID).First(&service).RecordNotFound(); notFound {
		// service id is not found
		log.Fatalf("User with service_id %s is not found.", requestStruct.ServiceID)
		c.AbortWithStatusJSON(404, gin.H{"message": "service id not found", "code": "not_found"})
	} else {
		// there's a user with such a service id and its info is stored at jwt.
		// now, check their entered password
		if err := bcrypt.CompareHashAndPassword([]byte(service.Password), []byte(requestStruct.Password)); err != nil {
			log.Fatalf("there is an error in the password %v", err)
			c.AbortWithStatusJSON(402, gin.H{"message": "wrong password entered", "code": "wrong_password"})
		}
		// the entered password is correct! Generate a token now, will you?
		key, err := generateSecretKey(50)
		if err != nil {
			// why the fuck people?
			c.AbortWithError(500, err)
		}
		token, err := generateJWT(key, service.ServiceName)
		if err != nil {
			c.AbortWithError(500, err)
		}

		// commit token onto Db and send it over the wire...
		service.JWT.SecretKey = string(key)
		service.JWT.CreatedAt = time.Now().UTC()
		db.Create(&service)
		c.Writer.Header().Set("Authorization", token)
		c.JSON(http.StatusOK, gin.H{"message": "authorization ok", "code": "authorization_ok"})

	}

}

func authorizationMiddleware(c *gin.Context) {
	// just handle the simplest case, authorization is not provided.
	auth := c.Request.Header.Get("Authorization")
	if auth == "" {
		// do something...
		c.AbortWithStatusJSON(401, gin.H{"message": "authorization not provided",
			"code": "unauthorized"})

	}
	// validate that the token
	// get the ServiceID from the request body, use Nopcloser
	// if invalid return 401, else 200, redirect.

	// I'm using a hash map because i'm lazy and there's no way to map all
	// of the request variants.

	var req map[string]string
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error", "code": "server_error"})
	} else {
		// get the authorization right of the request body
		// there is no way authorization is not propagated upto here.

		serviceID := req["service_name"]

		// db stuffs
		db, err := gorm.Open("sqlite3", "/tmp/gorm_db")

		if err != nil {
			log.Fatalf("There's an erron in DB connection, %v", err)
		}

		defer db.Close()

		var service Service
		if notfound := db.Preload("JWT").Where("service_name = ?", serviceID).First(&service).RecordNotFound(); notfound {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "user not found", "code": "not_found"})
		} else {
			// there's a user
			// validate their entered authorization
			if _, err := verifyJWT(service.JWT.SecretKey); err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"message": err, "code": "unauthorized"})
			} else {
				c.Next()
			}
		}

	}

}

func generateSecretKey(n int) ([]byte, error) {
	key := make([]byte, n)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}
