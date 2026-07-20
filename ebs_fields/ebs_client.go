package ebs_fields

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var log = logrus.New()

var ebsTransport = &http.Transport{
	TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
}

var ebsHTTPClient = &http.Client{
	Timeout:   3 * 30 * time.Second,
	Transport: otelhttp.NewTransport(ebsTransport),
}

// EBSHttpClient the client to interact with EBS
func EBSHttpClient(targetURL string, req []byte) (code int, ebsGenericResponse EBSParserFields, err error) {
	return EBSHttpClientWithClient(ebsHTTPClient, targetURL, req)
}

// EBSHttpClientWithClient sends an EBS request using the caller-provided HTTP client.
func EBSHttpClientWithClient(client *http.Client, targetURL string, req []byte) (code int, ebsGenericResponse EBSParserFields, err error) {
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

	if client == nil {
		code = http.StatusInternalServerError
		return code, ebsGenericResponse, errors.New("missing EBS HTTP client")
	}

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

	ebsResponse, err := client.Do(reqHandler)
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

	log.WithFields(logrus.Fields{"bytes": respSize}).Debug("EBS response received")
	if !strings.Contains(ebsResponse.Header.Get("Content-Type"), "application/json") {
		code = http.StatusInternalServerError
		log.WithFields(logrus.Fields{
			"code":    "wrong content type parsed",
			"details": ebsResponse.Header.Get("Content-Type"),
		}).Error("ebs response content type is not application/json")
		return code, ebsGenericResponse, ContentTypeErr
	}
	if err := json.Unmarshal(responseBody, &ebsGenericResponse); err != nil {
		log.WithFields(logrus.Fields{
			"code":           err.Error(),
			"response_bytes": len(responseBody),
		}).Info("ebs response transaction")
		code = http.StatusInternalServerError
		return code, ebsGenericResponse, err
	}
	if ebsGenericResponse.ResponseCode == 0 {
		code = http.StatusOK
		return code, ebsGenericResponse, nil
	}
	code = http.StatusBadGateway
	return code, ebsGenericResponse, errors.New(ebsGenericResponse.ResponseMessage)
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
