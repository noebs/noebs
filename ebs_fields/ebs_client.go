package ebs_fields

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var log = logrus.New()

var ebsTransport = &http.Transport{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
}

var ebsHTTPClient = &http.Client{
	Timeout:   3 * 30 * time.Second,
	Transport: otelhttp.NewTransport(ebsTransport),
}

// EBSHttpClient the client to interact with EBS
func EBSHttpClient(targetURL string, req []byte) (code int, ebsGenericResponse EBSParserFields, err error) {
	initEBSMetrics()
	start := time.Now()
	reqSize := len(req)
	respSize := 0
	endpointLabel := "unknown"
	targetLabel := "unknown"
	if parsed, parseErr := url.Parse(targetURL); parseErr == nil {
		if parsed.Path != "" {
			endpointLabel = parsed.Path
		}
		if parsed.Host != "" {
			targetLabel = parsed.Host
		}
	}
	defer func() {
		recordEBSMetrics(endpointLabel, targetLabel, http.MethodPost, code, err, reqSize, respSize, time.Since(start))
	}()

	log.WithFields(logrus.Fields{"url": targetURL, "bytes": reqSize}).Debug("EBS request")
	reqBuffer := bytes.NewBuffer(req)

	reqHandler, err := http.NewRequest(http.MethodPost, targetURL, reqBuffer)

	if err != nil {
		code = http.StatusInternalServerError
		log.WithFields(logrus.Fields{
			"code": err.Error(),
		}).Error("Error in establishing connection to the host")
		return code, ebsGenericResponse, err
	}
	reqHandler.Header.Set("Content-Type", "application/json")

	ebsResponse, err := ebsHTTPClient.Do(reqHandler)
	if err != nil {
		code = http.StatusGatewayTimeout
		log.WithFields(logrus.Fields{
			"code": err.Error(),
		}).Error("Error in establishing connection to the host")
		return code, ebsGenericResponse, EbsGatewayConnectivityErr
	}

	defer ebsResponse.Body.Close()
	responseBody, err := io.ReadAll(ebsResponse.Body)
	if err != nil {
		code = http.StatusInternalServerError
		log.WithFields(logrus.Fields{
			"code": err.Error(),
		}).Error("Error reading ebs response")
		return code, ebsGenericResponse, EbsGatewayConnectivityErr
	}
	respSize = len(responseBody)
	var c CacheCards
	var isValid = true
	c.Pan = getPan(req)

	log.WithFields(logrus.Fields{"bytes": respSize}).Debug("EBS response received")
	if !strings.Contains(ebsResponse.Header.Get("Content-Type"), "application/json") {
		code = http.StatusInternalServerError
		log.WithFields(logrus.Fields{
			"code":    "wrong content type parsed",
			"details": ebsResponse.Header.Get("Content-Type"),
		}).Error("ebs response content type is not application/json")
		return code, ebsGenericResponse, ContentTypeErr
	}
	var tmpRes IPINResponse
	if err := json.Unmarshal(responseBody, &ebsGenericResponse); err == nil {
		if ebsGenericResponse.ResponseCode == INVALIDCARD {
			isValid = false
		}
		c.IsValid = &isValid
		select {
		case EBSRes <- c:
		default:
		}
		if ebsGenericResponse.ResponseCode == 0 || strings.Contains(ebsGenericResponse.ResponseMessage, "Success") {
			code = http.StatusOK
			return code, ebsGenericResponse, nil
		} else {
			code = http.StatusBadGateway
			return code, ebsGenericResponse, errors.New(ebsGenericResponse.ResponseMessage)
		}
	} else {
		// there is an error in handling the incoming EBS's ebsResponse
		// log the err here please
		log.WithFields(logrus.Fields{
			"code":         err.Error(),
			"all_response": string(responseBody),
			"ebs_fields":   ebsGenericResponse,
		}).Info("ebs response transaction")
		if strings.Contains(err.Error(), " EBSParserFields.tranDateTime of type string") {
			json.Unmarshal(responseBody, &tmpRes)
			if tmpRes.ResponseCode == 0 || strings.Contains(tmpRes.ResponseMessage, "Success") {
				code = http.StatusOK
				return code, tmpRes.newResponse(), nil
			} else {
				code = http.StatusBadGateway
				return code, ebsGenericResponse, errors.New(ebsGenericResponse.ResponseMessage)
			}
		}
		code = http.StatusInternalServerError
		return code, ebsGenericResponse, err
	}

}

type IPINResponse struct {
	UUID            string `json:"UUID"`
	TranDateTime    int    `json:"tranDateTime"`
	ResponseMessage string `json:"responseMessage"`
	ResponseStatus  string `json:"responseStatus"`
	PubKeyValue     string `json:"pubKeyValue"`
	ResponseCode    int64  `json:"responseCode"`
	Pan             string `json:"pan"`
	ExpDate         string `json:"expDate"`
	Username        string `json:"userName"`
}

// newResponse the
func (i IPINResponse) newResponse() EBSParserFields {
	var res EBSResponse
	res.ResponseCode = int(i.ResponseCode)
	res.ResponseMessage = i.ResponseMessage
	res.PubKeyValue = i.PubKeyValue
	res.TranDateTime = strconv.Itoa(i.TranDateTime)
	res.UUID = i.UUID
	res.PAN = i.Pan
	res.ExpDate = i.ExpDate
	return EBSParserFields{EBSResponse: res}
}

var EBSRes = make(chan CacheCards)

func getPan(data []byte) string {
	var d map[string]any
	json.Unmarshal(data, &d)
	if res, ok := d["PAN"].(string); ok {
		return res
	}
	return ""
}

var (
	INVALIDPIN   = 53
	SUCCESS      = 0
	INVALIDCARD  = 52
	ROUTINGERROR = 72
)

type Configs struct {
	DB any
}
