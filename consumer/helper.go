package consumer

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/cards"
	"github.com/adonese/noebs/utils"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"

	"net/http"
)

//Routes get all consumer routes to be used in main noebs program
func Routes(groupName string, route *gin.Engine, db *gorm.DB, redisClient *redis.Client) {

	auth := &gateway.JWTAuth{}
	auth.Init()
	//FIXME #63 this is nul in JWTAuth
	state := State{Db: db, Redis: redisClient, Auth: auth, UserModel: gateway.UserModel{}, UserLogin: gateway.UserLogin{}}

	cv1 := route.Group(groupName)
	cv1.Use(state.APIAuth())

	ss := utils.Service{Redis: redisClient, Db: db}
	s := &Service{Service: ss}
	var cardServices = cards.Service{Redis: redisClient}

	{

		cv1.POST("/register", state.CreateUser)
		cv1.POST("/refresh", state.RefreshHandler)
		cv1.POST("/logout", state.LogOut)

		cv1.POST("/billers/new", s.NewBiller)
		cv1.POST("/balance", s.Balance)
		cv1.POST("/is_alive", s.IsAlive)
		cv1.POST("/bill_payment", s.BillPayment)
		cv1.POST("/bill_inquiry", s.BillInquiry)
		cv1.POST("/p2p", s.CardTransfer)
		cv1.POST("/cashIn", s.CashIn)
		cv1.POST("/cashOut", s.CashOut)
		cv1.POST("/account", s.AccountTransfer)
		cv1.POST("/purchase", s.Purchase)
		cv1.POST("/status", s.Status)
		cv1.POST("/key", s.WorkingKey)
		cv1.POST("/ipin", s.IPinChange)
		cv1.POST("/generate_qr", s.QRGeneration)
		cv1.POST("/qr_payment", s.QRPayment)
		cv1.POST("/generate_ipin", s.GenerateIpin)
		cv1.POST("/complete_ipin", s.CompleteIpin)
		cv1.POST("/payment_token/:payment", s.GeneratePaymentToken)
		cv1.POST("/payment_token", s.GeneratePaymentToken) // specific to sahil
		cv1.POST("/payment/:uuid", s.SpecialPayment)
		cv1.GET("/payment/:uuid", s.GetPaymentToken)
		cv1.GET("/merchant/i", s.BillerTrans)
		cv1.POST("/cancel", s.CancelBiller)
		cv1.GET("/info", s.info)
		cv1.POST("/info", s.info)
		cv1.POST("/vouchers/generate", s.GenerateVoucher)

		//cashout creation services
		cv1.POST("/cashout/register", s.RegisterCashout) //register biller as accepting cashouts
		cv1.POST("/cashout/profile", s.UpdateCashout)
		cv1.POST("/cashout/generate/:biller", s.GenerateCashoutClaim) //returns uuid to be used by merchant, in /cashout/claims
		cv1.POST("/cashout/claim/:biller", s.CashoutClaims)           // performs payment

		cv1.POST("/qr_refund", s.QRRefund)
		cv1.POST("/card_info", s.EbsGetCardInfo)
		cv1.POST("/pan_from_mobile", s.GetMSISDNFromCard)

		cv1.GET("/mobile2pan", s.CardFromNumber)
		cv1.GET("/nec2name", s.NecToName)

		cv1.POST("/login", state.LoginHandler)
		cv1.Use(auth.AuthMiddleware())
		cv1.GET("/get_cards", s.GetCards)
		cv1.POST("/add_card", s.AddCards)
		cv1.POST("/tokenize", cardServices.Tokenize)

		cv1.PUT("/edit_card", s.EditCard)
		cv1.DELETE("/delete_card", s.RemoveCard)

		cv1.GET("/get_mobile", s.GetMobile)
		cv1.POST("/add_mobile", s.AddMobile)

		//card issuance specifics
		cv1.POST("/cards/new", s.RegisterCard)
		cv1.POST("/cards/complete", s.CompleteRegistration)

		cv1.POST("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": true})
		})

	}
}

//func rateRpc() float32{
//	address := "192.168.20.21:50051"
//	conn, err := grpc.Dial(address, grpc.WithInsecure())
//	if err != nil {
//		log.Fatalf("did not connect: %v", err)
//	}
//	defer conn.Close()
//	c := pb.NewRaterClient(conn)
//
//	// Contact the server and print out its response.
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	r, err := c.GetSDGRate(ctx, &pb.Empty{})
//	if err != nil {
//		log.Fatalf("could not greet: %v", err)
//	}
//	log.Printf("Greeting: %f", r.Message)
//	return r.Message
//}

var (
	serverError       = errors.New("unable to connect to the DB")
	ErrCreateDbRow    = errors.New("unable to create a new db row/column")
	errNoServiceID    = errors.New("empty Service ID was entered")
	errObjectNotFound = errors.New("object not found")
)

func isMember(key, val string, r *redis.Client) bool {
	b, _ := r.SIsMember(key, val).Result()
	return b
}

// validatePassword to include at least one capital letter, one symbol and one number
// and that it is at least 8 characters long
func validatePassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	var hasUpper, hasSymbol, hasNumber bool
	// check if password contains @, &, #, $, %, ^, *, (, ), _, -, +, =, !, ?, ., /, <, >, [, ], {, }, |, \, ;, :, "
	if strings.ContainsAny(password, "@&#$%^*()_-+=!.?/<>[]:{}|\\;:\"") {
		hasSymbol = true
	}
	for _, c := range password {
		if unicode.IsUpper(c) {
			hasUpper = true
		}
		if unicode.IsSymbol(c) {
			hasSymbol = true
		}

		if unicode.IsNumber(c) {
			hasNumber = true
		}
	}
	return hasUpper && hasSymbol && hasNumber
}

//userHasSessions is a handy function we can use to check if a user has any active sessions e.g., esp
// when we don't want our users to use our app in new devices.
// we have to implement a way to:
// - identify user's devices
// - allow them to use the app in other devices in case their original device was lost
func userHasSessions(s *State, username string) bool {
	// Make sure the user doesn't have any active sessions!
	lCount, err := s.Redis.Get(username + ":logged_in_devices").Result()

	num, _ := strconv.Atoi(lCount)
	// Allow for the user to be logged in -- add allowance through someway
	if err != redis.Nil && num > 1 {
		// The user is already logged in somewhere else. Communicate that to them, clearly!
		//c.JSON(http.StatusBadRequest, gin.H{"code": "user_logged_elsewhere",
		//	"error": "You are logging from another device. You can only have one valid session"})
		//return
		log.Print("The user is logging from a different location")
		return true
	}
	return false
}

//userExceedMaxSessions keep track of many login-attempts a user has made
func userExceededMaxSessions(s *State, username string) bool {
	// make sure number of failed logged_in counts doesn't exceed the allowed threshold.
	res, err := s.Redis.Get(username + ":login_counts").Result()
	if err == redis.Nil {
		// the has just logged in
		s.Redis.Set(username+":login_counts", 0, time.Hour)
	} else if err == nil {
		count, _ := strconv.Atoi(res)
		if count >= 5 {
			// Allow users to use another login method (e.g., totp, or they should reset their password)
			// Lock their account
			//s.Redis.HSet(username, "suspecious_behavior", 1)
			s.Redis.HIncrBy(username, "suspicious_behavior", 1)
			ttl, _ := s.Redis.TTL(username + ":login_counts").Result()
			log.Printf("user exceeded max sessions", ttl)
			return true
		}
	}
	return false
}
