package merchant

import (
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
)

//AddBilling to a specific biller ID via `MerchantMobileNumber`
func (m Merchant) AddBilling(c *gin.Context) {
	c.BindJSON(&m)
	if m.BillerID == "" || m.MerchantMobileNumber == "" {
		verr := ebs_fields.ValidationError{Code: "not_found", Message: "empty_biller"}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	if err := m.db.Exec("update merchants set biller_id = ? where mobile = ?", m.BillerID, m.MerchantMobileNumber).Error; err != nil {
		m.log.Printf("error in updating billers: %v", err)
		verr := ebs_fields.ValidationError{Code: "db_err", Message: err.Error()}
		c.JSON(http.StatusInternalServerError, verr)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "ok"})

}

//Update to a specific biller ID via `MerchantMobileNumber`
func (m Merchant) Update(c *gin.Context) {
	c.BindJSON(&m)
	if m.BillerID == "" {
		verr := ebs_fields.ValidationError{Code: "not_found", Message: "empty_biller"}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	//TODO(adonese): omit fields in update. Could be dangerous.
	if err := m.db.Table("merchants").Where("biller_id = ?", m.BillerID).Update(&m).Error; err != nil {
		verr := ebs_fields.ValidationError{Code: "not_found", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "ok"})
}

// func (m Merchant) DummyTransaction(c *gin.Context) {
// 	// Set up a connection to the server.
// 	conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
// 	if err != nil {
// 		log.Fatalf("did not connect: %v", err)
// 		verr := ebs_fields.ValidationError{Code: "rpc_err", Message: err.Error()}
// 		c.JSON(http.StatusBadRequest, verr)
// 		return

// 	}
// 	defer conn.Close()
// 	cc := pb.NewPaymentAPIClient(conn)

// 	// Contact the server and print out its response.

// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	req2 := &pb.ConsumerKeyRequest{
// 		Datetime:      "",
// 		UUID:          "",
// 		ApplicationID: "",
// 	}

// 	ctx2, cancel2 := context.WithTimeout(context.Background(), 400*time.Second)

// 	defer cancel2()
// 	res1, err := cc.GetConsumerKey(ctx2, req2)
// 	if err != nil {
// 		log.Fatalf("did not connect: %v", err)
// 		verr := ebs_fields.ValidationError{Code: "rpc_err", Message: err.Error()}
// 		c.JSON(http.StatusBadRequest, verr)
// 		return
// 	}
// 	print(res1)
// 	ctx2, cancel2 = context.WithTimeout(context.Background(), 400*time.Second)
// 	defer cancel2()

// 	res, err := cc.Encrypt(ctx2, nil)

// 	req1 := &pb.SpecialPaymentRequest{
// 		TranDateTime:  "",
// 		UUID:          "",
// 		ApplicationID: "",
// 		Pan:           "",
// 		Expdate:       "",
// 		IPin:          "",
// 		Currency:      "",
// 		Amount:        0.0,
// 		BillerID:      "",
// 	}
// 	print(res)
// 	res2, err := cc.GetSpecialPayment(ctx, req1)
// 	if err != nil {
// 		log.Fatalf("could not greet: %v", err)
// 	}
// 	log.Printf("grpc response is: %v", res2)
// }

//GetMerchant from existing merchants in noebs
func (m Merchant) GetMerchant(c *gin.Context) {
	id := c.DefaultQuery("id", "")

	merchant, err := m.get(id)
	if err != nil {
		verr := ebs_fields.ValidationError{Code: "request_error", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}
	c.JSON(http.StatusOK, merchant)
}

// Login creates new noebs merchant
func (m Merchant) Login(c *gin.Context) {
	if err := c.ShouldBindJSON(&m); err != nil {
		verr := ebs_fields.ValidationError{Code: "request_error", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}
	merchant, err := m.get(m.MerchantID)
	if err != nil {
		verr := ebs_fields.ValidationError{Code: "db_err", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}
	c.JSON(http.StatusCreated, merchant)
}
