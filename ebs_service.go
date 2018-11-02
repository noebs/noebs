package main

import (
	"fmt"
	"net/http"
	"io/ioutil"
	"time"
	"crypto/tls"
	"reflect"
	"github.com/gin-gonic/gin"
	"gopkg.in/go-playground/validator.v8"
	"github.com/gin-gonic/gin/binding"
	"bytes"
)

func main() {

	route := gin.Default()
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		v.RegisterStructValidation(workingKeyStructValidators, WorkingKeyFields{})
	}

	route.POST("/workingKey", WorkingKey)
	route.POST("/test", func(context *gin.Context) {
		context.JSON(http.StatusOK, gin.H{"message": true,})
	})
	route.Run("0.0.0.0:3333")
	}

type PurchaseFields struct{
	SystemTraceAuditNumber int `validator:"systemTraceAuditNumber" binding:"required"`
	TranDateTime time.Time `validator:"tranDateTime" binding:"required"`
	TerminalID string `validator:"terminalId" binding:"required"`
	ClientID string `validator:"clientId" binding:"required"`
	TranAmount float32 `validator:"tranAmount" binding:"required"`
	Pan string `validator:"PAN" binding:"required"`
	Pin string `validator:"PIN" binding:"required"`
	ExpDate string `validator:"expDate" binding:"required"`
}

func workingKeyStructValidators(validate *validator.Validate, structLevel *validator.StructLevel){
	workingKey := structLevel.CurrentStruct.Interface().(WorkingKeyFields)
	if len(workingKey.TerminalID) != 8{
		structLevel.ReportError(
			reflect.ValueOf(workingKey.TerminalID), "terminalId", "length_8", "tag_8")
	}
}

type WorkingKeyFields struct {
	SystemTraceAuditNumber int `validator:"systemTraceAuditNumber" binding:"required"`
	TranDateTime time.Time `validator:"tranDateTime" binding:"required"`
	TerminalID string `validator:"terminalId" binding:"required"`
	ClientID string `validator:"clientId" binding:"required"`
}


func WorkingKey(c *gin.Context){

	url := "https://212.0.129.118/terminal_api/workingKey/"
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.


	var fields = WorkingKeyFields{}
	reqBody, err := ioutil.ReadAll(c.Request.Body)
	reader1 := ioutil.NopCloser(bytes.NewBuffer([]byte(reqBody)))
	reader2 := ioutil.NopCloser(bytes.NewBuffer([]byte(reqBody)))

	c.Request.Body = reader1
	if err != nil{
		fmt.Println("There's an error in nopclose init.")
		c.AbortWithError(500, err)
	}
	if wkeyErr := c.ShouldBindBodyWith(&fields, binding.JSON); wkeyErr == nil{
		c.Request.Body = reader2
		EBSHttpClient(url, c)
	}else{
	c.JSON(http.StatusBadRequest, gin.H{"message": "wronffffg", "error": wkeyErr.Error()})
	}

	defer c.Request.Body.Close()


}

func Purchase(c *gin.Context){
	url := "https://212.0.129.118/terminal_api/Purchase/"
	// consume the request here and pass it over onto the EBS.
	requestBody, err := ioutil.ReadAll(c.Request.Body)
	defer c.Request.Body.Close()
	if err != nil{
		fmt.Println("somethong's wrong with the request.")
		c.JSON(400, requestBody)
	}
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = WorkingKeyFields{}

	if err := c.ShouldBindJSON(&fields); err == nil{
		EBSHttpClient(url, c)
	}else{
		c.JSON(http.StatusBadRequest, gin.H{"message": "wrong", "error": err.Error()})
	}

}


// a generic client for EBS's
func EBSHttpClient(url string, c *gin.Context){

	verifyTLS := &http.Transport{
		TLSClientConfig:&tls.Config{InsecureSkipVerify:true},
	}

	ebsClient := http.Client{
		Timeout: 30 * time.Second,
		Transport:verifyTLS,
	}

	reqHandler, err := http.NewRequest("POST", url, c.Request.Body)
	if err != nil{
		fmt.Println(err.Error())
	}
	reqHandler.Header.Set("Content-Type", "application/json")
	reqHandler.Header.Set("API-Key", "5d6f54d4-3af4-4ffc-b78d-c2f1ca7827d9") // For Morsal case only.

	response, err := ebsClient.Do(reqHandler)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Unable to reach EBS.",
		"error": err.Error(),})
		return
	}

	responseBody, err := ioutil.ReadAll(response.Body)
	// else, the response is really working!
	if err != nil{
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Unable to reach EBS.",
			"error": err.Error(),})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Ok",
		"response": string(responseBody),})


	defer response.Body.Close()

}