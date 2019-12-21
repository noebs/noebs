package consumer

import (
	"context"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	pb "rateit/rate"
	"net/http"
	"time"
)

func ConsumerRoutes(groupName string, route *gin.Engine) {
	cv1 := route.Group(groupName)
	cv1.Use(gateway.ApiAuth())
	{

		cv1.POST("/register", gateway.CreateUser)
		cv1.POST("/refresh", gateway.RefreshHandler)
		cv1.POST("/logout", gateway.LogOut)

		cv1.POST("/balance", ConsumerBalance)
		cv1.POST("/is_alive", ConsumerIsAlive)
		cv1.POST("/bill_payment", ConsumerBillPayment)
		cv1.POST("/bill_inquiry", ConsumerBillInquiry)
		cv1.POST("/p2p", ConsumerCardTransfer)
		cv1.POST("/purchase", ConsumerPurchase)
		cv1.POST("/status", ConsumerStatus)
		cv1.POST("/key", ConsumerWorkingKey)
		cv1.POST("/ipin", ConsumerIPinChange)
		cv1.POST("/generate_qr", QRGeneration)
		cv1.POST("/qr_payment", QRPayment)
		cv1.POST("/generate_ipin", ConsumerGenerateIpin)
		cv1.POST("/complete_ipin", ConsumerCompleteIpin)

		cv1.POST("/qr_refund", QRRefund)
		cv1.GET("/mobile2pan", CardFromNumber)
		cv1.GET("/nec2name", EelToName)

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


func rateRpc() float32{
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()
	c := pb.NewRaterClient(conn)

	// Contact the server and print out its response.

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := c.GetSDGRate(ctx, &pb.Empty{})
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	log.Printf("Greeting: %f", r.Message)
	return r.Message
}