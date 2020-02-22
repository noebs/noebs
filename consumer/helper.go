package consumer

import (
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/gin-gonic/gin"

	"net/http"
)

//Routes get all consumer routes to be used in main noebs program
func Routes(groupName string, route *gin.Engine) {
	cv1 := route.Group(groupName)
	cv1.Use(gateway.APIAuth())
	{

		cv1.POST("/register", gateway.CreateUser)
		cv1.POST("/refresh", gateway.RefreshHandler)
		cv1.POST("/logout", gateway.LogOut)

		cv1.POST("/balance", Balance)
		cv1.POST("/is_alive", IsAlive)
		cv1.POST("/bill_payment", BillPayment)
		cv1.POST("/bill_inquiry", BillInquiry)
		cv1.POST("/p2p", CardTransfer)
		cv1.POST("/purchase", Purchase)
		cv1.POST("/status", Status)
		cv1.POST("/key", WorkingKey)
		cv1.POST("/ipin", IPinChange)
		cv1.POST("/generate_qr", QRGeneration)
		cv1.POST("/qr_payment", QRPayment)
		cv1.POST("/generate_ipin", GenerateIpin)
		cv1.POST("/complete_ipin", CompleteIpin)
		cv1.POST("/payment_token", GeneratePaymentToken)
		cv1.POST("/payment/:uuid", SpecialPayment)
		cv1.GET("/payment/:uuid", GetPaymentToken)

		cv1.POST("/qr_refund", QRRefund)
		cv1.GET("/mobile2pan", CardFromNumber)
		cv1.GET("/nec2name", NecToName)

		cv1.POST("/login", gateway.LoginHandler)
		cv1.Use(gateway.AuthMiddleware())
		cv1.GET("/get_cards", GetCards)
		cv1.POST("/add_card", AddCards)

		cv1.PUT("/edit_card", EditCard)
		cv1.DELETE("/delete_card", RemoveCard)

		cv1.GET("/get_mobile", GetMobile)
		cv1.POST("/add_mobile", AddMobile)

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
