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
	"encoding/json"
	"strconv"
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

	url := "path/to/ebs/endpoint"	// EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
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
	url := "uri/for/this_ebs_endpoint"
	//FIXME: the URL should be composable of the IP and the endpoint uri
	// - it should also be provided through a struct or in systems env
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
	reqHandler.Header.Set("API-Key", "removeme") // For Morsal case only.
	// EBS doesn't impose any sort of API-keys or anything. Typical EBS.

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

	var ebsResponse map[string]string
	if err := json.Unmarshal(responseBody, &ebsResponse); err == nil{
		// there's no problem in Unmarshalling
		if responseCode, ok := ebsResponse["responseCode"]; ok { //Frankly, if we went this far it will work anyway.
			resCode, err := strconv.Atoi(string(responseCode))
			if err != nil{
				c.JSON(http.StatusInternalServerError, "There's a problem. Check again later.") //Fixme.
			}
			if resCode == 0{
				// It's a successful transaction! Fuck it.
				c.JSON(http.StatusOK, responseBody)
			}
		} else {
			// Nope, it is not a successful transaction. You screwed.
			c.JSON(http.StatusBadRequest, responseBody) // return the response as it is.
		}
		// There's an error in Unmarshalling the responseBody. Highly unlikely though. I, screwed.
		c.JSON(http.StatusInternalServerError, "There's a problem. Check again later.") //Fixme.
	}
	
	defer response.Body.Close()

}