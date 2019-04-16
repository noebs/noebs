package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adonese/noebs/ebs_fields"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

//EBSHttpClient the client to interact with EBS
func EBSHttpClient(url string, req []byte) (int, ebs_fields.GenericEBSResponseFields, error) {

	verifyTLS := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	ebsClient := http.Client{
		Timeout:   30 * time.Second,
		Transport: verifyTLS,
	}

	reqBuffer := bytes.NewBuffer(req)

	var ebsGenericResponse ebs_fields.GenericEBSResponseFields

	if !UseMockServer {
		reqHandler, err := http.NewRequest(http.MethodPost, url, reqBuffer)

		if err != nil {
			fmt.Println(err.Error())
			return 500, ebsGenericResponse, err
		}
		reqHandler.Header.Set("Content-Type", "application/json")

		ebsResponse, err := ebsClient.Do(reqHandler)
		if err != nil {
			log.Printf("I couldn't make it into EBS")
			return http.StatusBadGateway, ebsGenericResponse, ebsGatewayConnectivityErr
		}

		defer ebsResponse.Body.Close()

		responseBody, err := ioutil.ReadAll(ebsResponse.Body)
		if err != nil {
			log.Printf("I couldn't make it into EBS")
			// wrong. make the error of any other type than ebsGatewayConnectivityErr
			return 500, ebsGenericResponse, ebsGatewayConnectivityErr
		}
		log.Println(string(responseBody))

		// check Content-type is application json, if not, panic!
		if ebsResponse.Header.Get("Content-Type") != "application/json" {
			// panic
			return 500, ebsGenericResponse, contentTypeErr
		}

		if err := json.Unmarshal(responseBody, &ebsGenericResponse); err == nil {
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
			// log the err here please
			log.Printf("There is an error in EBS: %v. The res struct is: %+v", err, ebsGenericResponse)
			log.Printf("The error is: %s\n", err.Error())
			return 500, ebsGenericResponse, err
		}

	} else {
		// mock settings.

		MockEbsResponse(urlToMock(url), &ebsGenericResponse)
		fmt.Printf("the ebs generic respons is %s", ebsGenericResponse.MiniStatementRecords)
		return 200, ebsGenericResponse, nil
	}

}

func urlToMock(url string) interface{} {
	if url == EBSMerchantIP+BalanceEndpoint {
		return mockPurchaseResponse{}
	} else if url == EBSMerchantIP+PurchaseEndpoint {
		return mockPurchaseResponse{}

	} else if url == EBSMerchantIP+MiniStatementEndpoint {
		return mockMiniStatementResponse{}

	} else if url == EBSMerchantIP+WorkingKeyEndpoint {
		fmt.Printf("i'm here..")
		return mockWorkingKeyResponse{}
	}
	return mockWorkingKeyResponse{}
}
