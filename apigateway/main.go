package gateway

import (
	"errors"
	"fmt"
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
	route.POST("/create", CreateServiceID)
	route.POST("/get_service", GetServiceID)

	auth := route.Group("/admin", authMiddleware())

	auth.POST("/test", func(context *gin.Context) {
		context.JSON(http.StatusOK, gin.H{"message": true, "code": "ok"})
	})

	return route
}

func main() {
	r := GetMainEngine()
	r.Run(":8001")
}

func LoginHandler(c *gin.Context) {

	var req UserModel
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		// The request is wrong
		log.Printf("The request is wrong. %v", err)
		c.AbortWithStatusJSON(400, gin.H{"message": "Bad UserRequest", "code": "bad_request"})
	}

	// Now, the request is well valid. Let us check its credentials.

	//db connection. Not good.
	db, err := gorm.Open("sqlite3", "test.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	// do the Models migrations here. The ones you will be using
	db.AutoMigrate(&Service{}, &JWT{}, &UserModel{})

	log.Printf("the processed request is: %v\n", req)
	var u UserModel

	if notFound := db.Preload("JWT").Where("service_id = ?", req.ServiceID).First(&u).RecordNotFound(); notFound {
		// service id is not found
		log.Printf("User with service_id %s is not found.", req.ServiceID)
		c.AbortWithStatusJSON(404, gin.H{"message": notFound, "code": "not_found"})
		return
	}

	// there's a user with such a service id and its info is stored at jwt. celebrity
	// now, check their entered password
	log.Printf("passwords are: %v, %v\n", u.Password, req.Password)

	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)); err != nil {
		log.Printf("there is an error in the password %v", err)
		c.AbortWithStatusJSON(402, gin.H{"message": "wrong password entered", "code": "wrong_password"})
		return
	}
	// the entered password is correct! Generate a token now, will you?
	key, err := generateSecretKey(50)
	if err != nil {
		// why the fuck people?
		c.AbortWithError(500, err)
	}
	token, err := generateJWT(u.ServiceID)
	if err != nil {
		c.AbortWithError(500, err)
	}
	//
	//// commit token onto Db and send it over the wire...

	u.JWT.SecretKey = string(key)
	u.JWT.CreatedAt = time.Now().UTC()

	err = db.Save(&u).Error
	if err != nil {
		c.AbortWithStatusJSON(500, err.Error())
		return
	}
	c.Writer.Header().Set("Authorization", token)
	c.JSON(http.StatusOK, gin.H{"message": "authorization ok", "code": "authorization_ok"})

}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		// just handle the simplest case, authorization is not provided.
		h := c.GetHeader("Authorization")
		if h == "" {
			c.AbortWithStatusJSON(401, gin.H{"message": "empty header was sent",
				"code": "unauthorized"})
			return

		}
		// db stuffs
		db, err := gorm.Open("sqlite3", "test.db")

		if err != nil {
			log.Printf("There's an erron in DB connection, %v", err)
			c.AbortWithError(500, err)
			return
		}
		defer db.Close()

		var service TokenClaims
		
		claim, err := verifyJWT(h, &service)
		if err != nil {
			c.AbortWithStatusJSON(400, gin.H{"message": err.Error()})
			return
		}
		log.Printf("The claim object is %v, The claims are: %v", claim.Valid(), service)

		c.Next()
	}

}

func generateSecretKey(n int) ([]byte, error) {
	key := make([]byte, n)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

func CreateServiceID(c *gin.Context) {
	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": serverError.Error()})
		return
	}

	defer db.Close()

	var u UserModel
	if err := db.AutoMigrate(&UserModel{}).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}

	err = c.ShouldBindBodyWith(&u, binding.JSON)
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}

	fmt.Printf("Raw password is: %v\n", u)

	err = u.hashPassword()
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
	}
	fmt.Printf("Raw password is: %v\nLength is: %v", u, len(u.Password))

	if err := db.Create(&u).Error; err != nil {
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": "object was successfully created", "details": u})
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

	if err := db.Where("service_name = ?", id).First(&res).Error; err != nil {
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
