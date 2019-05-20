package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/sirupsen/logrus"
	"io/ioutil"
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
			log.WithFields(logrus.Fields{
				"error": err.Error(),
			}).Error("Error in establishing connection to the host")
			return 500, ebsGenericResponse, err
		}
		reqHandler.Header.Set("Content-Type", "application/json")

		ebsResponse, err := ebsClient.Do(reqHandler)
		if err != nil {
			log.WithFields(logrus.Fields{
				"error": err.Error(),
			}).Error("Error in establishing connection to the host")
			return http.StatusGatewayTimeout, ebsGenericResponse, ebsGatewayConnectivityErr
		}

		defer ebsResponse.Body.Close()

		responseBody, err := ioutil.ReadAll(ebsResponse.Body)
		if err != nil {
			log.WithFields(logrus.Fields{
				"error": err.Error(),
			}).Error("Error reading ebs response")

			return 500, ebsGenericResponse, ebsGatewayConnectivityErr
		}

		// check Content-type is application json, if not, panic!
		if ebsResponse.Header.Get("Content-Type") != "application/json" {
			log.WithFields(logrus.Fields{
				"error":   "wrong content type parsed",
				"details": ebsResponse.Header.Get("Content-Type"),
			}).Error("ebs response content type is not application/json")
			return 500, ebsGenericResponse, contentTypeErr
		}
		if err := json.Unmarshal(responseBody, &ebsGenericResponse); err == nil {
			// there's no problem in Unmarshalling
			if ebsGenericResponse.ResponseCode == 0 {
				return http.StatusOK, ebsGenericResponse, nil
			} else {

				err := errors.New(ebsGenericResponse.ResponseMessage)
				return http.StatusBadGateway, ebsGenericResponse, err
			}

		} else {
			// there is an error in handling the incoming EBS's ebsResponse
			// log the err here please
			log.WithFields(logrus.Fields{
				"error": err.Error(),
			}).Info("ebs response transaction")

			return http.StatusInternalServerError, ebsGenericResponse, err
		}

	} else {
		// mock settings.

		MockEbsResponse(urlToMock(url), &ebsGenericResponse)
		return http.StatusOK, ebsGenericResponse, nil
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
