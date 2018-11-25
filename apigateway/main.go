package gateway

import (
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/jinzhu/gorm"
	"golang.org/x/crypto/bcrypt"
	"log"
	"net/http"
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

func main() {
	//
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
	db, err := gorm.Open("sqlite", "/tmp/gorm_db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	var service ServiceModel
	hashedPassword := requestStruct.HashPassword()
	if err := db.Where("service_id = ?", requestStruct.ServiceID).First(&service); err != nil {
		// service id is not found
		log.Fatalf("User with service_id %s is not found. Error: %v", requestStruct.ServiceID, err)
		c.AbortWithStatusJSON(404, gin.H{"message": "service id not found", "code": err})
	}

	// Check their entered password
	if err := bcrypt.CompareHashAndPassword([]byte(requestStruct.Password), hashedPassword); err != nil {
		// user has entered a wrong password
		log.Fatalf("Password hashing is wrong, %v", err)
		c.AbortWithStatusJSON(401, gin.H{"message": "Wrong password was entered", "code": "wrong_password"})
	}

	// if we are here, that means
	// Returns a valid JWT token in the response header

	// generate JWT
	var jwtModel JWTModel

	if token, err := generateJWT(requestStruct.ServiceID); err != nil{
		// This is likely internal server error?
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": "internal server error", "code": "server_error"})
	} else {
		// the secret key must be committed into the jwt table
		if err := db.Where("service_id = ?", requestStruct.ServiceID).First(&jwtModel); err != nil {
			// service id is not found
			log.Fatalf("User with service_id %s is not found. Error: %v", requestStruct.ServiceID, err)
			c.AbortWithStatusJSON(404, gin.H{"message": "service id not found", "code": err})
		}
		c.JSON(http.StatusOK, gin.H{"authorization": "access_token " + token})
	}
}
