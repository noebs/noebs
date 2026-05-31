package utils

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

var (
	ErrMissingSMSConfig  = errors.New("missing sms config")
	ErrSMSDeliveryFailed = errors.New("sms delivery failed")
	defaultSMSHTTPClient = &http.Client{Timeout: 10 * time.Second}
)

// SendSMS a generic function to send sms to any user
func SendSMS(noebsConfig *ebs_fields.NoebsConfig, sms SMS) error {
	if noebsConfig == nil {
		return ErrMissingSMSConfig
	}
	log.Printf("sending sms to %s", maskMobile(sms.Mobile))
	v := url.Values{}
	v.Add("api_key", noebsConfig.SMSAPIKey)
	v.Add("from", noebsConfig.SMSSender)
	v.Add("to", "249"+strings.TrimPrefix(sms.Mobile, "0"))
	v.Add("sms", sms.Message+"\n\n"+noebsConfig.SMSMessage)
	endpoint := noebsConfig.SMSGateway + v.Encode()
	res, err := defaultSMSHTTPClient.Get(endpoint)
	if err != nil {
		log.Printf("The error is: %+v", err)
		return fmt.Errorf("%w: %v", ErrSMSDeliveryFailed, err)
	}
	defer res.Body.Close()
	log.Printf("sms response status=%s", res.Status)
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: gateway returned %s", ErrSMSDeliveryFailed, res.Status)
	}
	return nil
}

// MaskPAN returns a masked string of the PAN
func MaskPAN(PAN string) string {
	length := len(PAN)
	if length < 10 {
		return PAN
	}
	PAN = PAN[:6] + "*****" + PAN[length-4:]
	return PAN
}

func maskMobile(mobile string) string {
	if mobile == "" {
		return ""
	}
	if len(mobile) <= 4 {
		return "****"
	}
	return "****" + mobile[len(mobile)-4:]
}
