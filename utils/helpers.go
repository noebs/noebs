package utils

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
)

func GetOrDefault(keys map[string]interface{}, key, def string) (string, bool) {
	value, ok := keys[key]
	if !ok {
		return def, ok
	}
	return value.(string), ok
}

// SendSMS a generic function to send sms to any user
func SendSMS(noebsConfig *ebs_fields.NoebsConfig, sms SMS) error {
	log.Printf("the message is: %+v", sms)
	v := url.Values{}
	v.Add("api_key", noebsConfig.SMSAPIKey)
	v.Add("from", noebsConfig.SMSSender)
	v.Add("to", "249"+strings.TrimPrefix(sms.Mobile, "0"))
	v.Add("sms", sms.Message+"\n\n"+noebsConfig.SMSMessage)
	url := noebsConfig.SMSGateway + v.Encode()
	log.Printf("the url is: %+v", url)
	res, err := http.Get(url)
	if err != nil {
		log.Printf("The error is: %+v", err)
		return err
	}
	log.Printf("The response body is: %v", res)
	return nil
}

// MaskPAN returns a masked string of the PAN
func MaskPAN(PAN string) string {
	length := len(PAN)
	PAN = PAN[:6] + "*****" + PAN[length-4:]
	return PAN
}
