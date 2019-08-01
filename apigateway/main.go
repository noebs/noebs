package gateway

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/jinzhu/gorm"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	"math/rand"
	"net/http"
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

	if notFound := db.Preload("JWT").Where("username = ?", req.Username).First(&u).RecordNotFound(); notFound {
		// service id is not found
		log.Printf("User with service_id %s is not found.", req.Username)
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
	token, err := GenerateJWT(u.Username)
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

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		// just handle the simplest case, authorization is not provided.
		h := c.GetHeader("Authorization")
		if h == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "empty header was sent",
				"code": "unauthorized"})
			return

		}

		var service TokenClaims

		_, err := VerifyJWT(h, &service)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error()})
			return
		}
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
		c.AbortWithStatusJSON(500, gin.H{"message": err.Error()})
		return
	}

	// make sure that the user doesn't exist in the database

	err = u.hashPassword()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}

	if err := db.Create(&u).Error; err != nil {
		// unable to create this user; see possible reasons
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error()})
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
