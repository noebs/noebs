package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adonese/noebs/validations"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)



func EBSHttpClient(url string, req []byte) (int, validations.GenericEBSResponseFields, error) {

	verifyTLS := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	ebsClient := http.Client{
		Timeout:   30 * time.Second,
		Transport: verifyTLS,
	}

	reqBuffer := bytes.NewBuffer(req)

	var ebsGenericResponse validations.GenericEBSResponseFields

	if !TEST{
		reqHandler, err := http.NewRequest(http.MethodPost, url, reqBuffer)

		if err != nil {
			fmt.Println(err.Error())
			return 500, ebsGenericResponse, err
		}
		reqHandler.Header.Set("Content-Type", "application/json")
		reqHandler.Header.Set("API-Key", "removeme") // For Morsal case only.
		// EBS doesn't impose any sort of API-keys or anything. Typical EBS.

		ebsResponse, err := ebsClient.Do(reqHandler)
		if err != nil {
			return 500, ebsGenericResponse, ebsGatewayConnectivityErr
		}

		defer ebsResponse.Body.Close()

		responseBody, err := ioutil.ReadAll(ebsResponse.Body)
		if err != nil {
			return 500, ebsGenericResponse, ebsGatewayConnectivityErr
		}
		log.Println(string(responseBody))

		// check Content-type is application json, if not, panic!
		if ebsResponse.Header.Get("Content-Type") != "application/json" {
			// panic
			return 500, ebsGenericResponse, contentTypeErr
		}
		if err := json.Unmarshal(responseBody, ebsGenericResponse); err == nil {
			// there's no problem in Unmarshalling
			if ebsGenericResponse.ResponseCode == 0 {
				// the transaction is successful
				return 200, ebsGenericResponse, nil

			} else {
				// there is an error in the transaction
				err := errors.New(ebsGenericResponse.ResponseMessage)
				return 400, ebsGenericResponse, err
			}

		} else {
			// there is an error in handling the incoming EBS's ebsResponse
			return 500, ebsGenericResponse, err
		}


	} else {
		// mock settings.

		MockEbsResponse(urlToMock(url), &ebsGenericResponse)
		fmt.Printf("the ebs generic respons is %s", ebsGenericResponse.MiniStatementRecords)
		return 200, ebsGenericResponse, nil
	}

}


func urlToMock(url string) interface{}{
	if url == EBSMerchantIP + BalanceEndpoint{
		return mockPurchaseResponse{}
	} else if url == EBSMerchantIP +PurchaseEndpoint{
		return mockPurchaseResponse{}

	} else if url == EBSMerchantIP + MiniStatementEndpoint{
		return mockMiniStatementResponse{}

	} else if url == EBSMerchantIP + WorkingKeyEndpoint{
		fmt.Printf("i'm here..")
		return mockWorkingKeyResponse{}
	}
	return mockWorkingKeyResponse{}
}