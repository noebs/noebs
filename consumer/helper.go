// Package consumer contains all of apis regarding EBS Consumer Web services
// the package is structured in such a way that separetes between the payment apis
// and the [services] apis.
//
// Payment APIs
// All of the payment apis are in [payment_apis.go] file, they include basically all of
// EBS Consumer web service docs [v3.0.0].
//
// Helper APIs
// We also have help apis in [services.go]
package consumer

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"github.com/pquerna/otp/totp"
)

var (
	serverError       = errors.New("unable to connect to the DB")
	ErrCreateDbRow    = errors.New("unable to create a new db row/column")
	errNoServiceID    = errors.New("empty Service ID was entered")
	errObjectNotFound = errors.New("object not found")
)

// validatePassword to include at least one capital letter, one symbol and one number
// and that it is at least 8 characters long
func validatePassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	var hasUpper, hasSymbol, hasNumber bool
	// check if password contains @, &, #, $, %, ^, *, (, ), _, -, +, =, !, ?, ., /, <, >, [, ], {, }, |, \, ;, :, "
	if strings.ContainsAny(password, "@&#$%^*()_-+=!.?/<>[]:{}|\\;:\"") {
		hasSymbol = true
	}
	for _, c := range password {
		if unicode.IsUpper(c) {
			hasUpper = true
		}
		if unicode.IsSymbol(c) {
			hasSymbol = true
		}

		if unicode.IsNumber(c) {
			hasNumber = true
		}
	}
	return hasUpper && hasSymbol && hasNumber
}

func generateOtp(secret string) (string, error) {
	passcode, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		return "", err
	}
	return passcode, nil
}

func (s *Service) ToDatabasename(url string) string {
	data := map[string]string{
		// url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardInfo
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerIsAliveEndpoint:         "alive",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerWorkingKeyEndpoint:      "public_key",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBalanceEndpoint:         "balance",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillInquiryEndpoint:     "bill_inquiry",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillPaymentEndpoint:     "bill_payment",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardTransferEndpoint:    "card_transfer",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerAccountTransferEndpoint: "account_transfer",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPayeesListEndpoint:      "payees_list",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerChangeIPinEndpoint:      "change_ipin",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPurchaseEndpoint:        "purchase",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerStatusEndpoint:          "status",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRPaymentEndpoint:       "qr_purchase", // the fuck is wrong with you guys
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRRefundEndpoint:        "qr_refund",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPANFromMobile:           "msisdn_pan",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardInfo:                "customer_info",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerGenerateVoucher:         "generate_voucher",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCashInEndpoint:          "cashin",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCashOutEndpoint:         "cashout",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerComplete:                "complete_tran",
		s.NoebsConfig.ConsumerIP + ebs_fields.IPinGeneration:                  "generate_ipin",
		s.NoebsConfig.ConsumerIP + ebs_fields.IPinCompletion:                  "ipin_completion",
		s.NoebsConfig.ConsumerIP + ebs_fields.MerchantTransactionStatus:       "merchant_status",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerRegister:                "register",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCompleteRegistration:    "complete_card_issuance",
	}
	return data[url]
}

// The buffer of the tranData channel must be greater than the maximum number of
// concurrnet clients that are connected to all services that use this channel
var tranData = make(chan PushData, 2048)

func (s *Service) Pusher(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-tranData:
			if !ok {
				return
			}
			// In the case we want to send a push notification to the receipient
			//  (typically for telecom operations, or any operation that a user adds a phone number in the transfer field)
			// But the problem, is that we have lost the reference to the original sender
			s.Logger.Infof("push queued type=%s uuid=%s", data.Type, data.UUID)
			// we are doing too much of db and logic here, let's simplify it
			if data.Phone != "" {
				tenantID := s.NoebsConfig.DefaultTenantID
				if tenantID == "" {
					tenantID = store.DefaultTenantID
				}
				user, err := s.Store.GetUserByMobile(ctx, tenantID, data.Phone)
				if err != nil {
					// not a tutipay user
					utils.SendSMS(&s.NoebsConfig, utils.SMS{Mobile: data.Phone, Message: data.Body})
				} else {
					data.To = user.DeviceID
					data.EBSData = ebs_fields.EBSResponse{}
					data.UserMobile = user.Mobile
					record := ebs_fields.PushDataRecord{
						UUID:           data.UUID,
						Type:           data.Type,
						Date:           data.Date,
						To:             data.To,
						Title:          data.Title,
						Body:           data.Body,
						CallToAction:   data.CallToAction,
						Phone:          data.Phone,
						IsRead:         data.IsRead,
						DeviceID:       data.DeviceID,
						UserMobile:     data.UserMobile,
						PaymentRequest: data.PaymentRequest,
					}
					_ = s.Store.CreatePushData(ctx, tenantID, &record)
					s.SendPush(ctx, data)
					// FIXME(adonese): fallback option, maybe there is not need for the duplication
					data.To = data.DeviceID // Sender DeviceID
					s.SendPush(ctx, data)
				}
			} else {
				tenantID := s.NoebsConfig.DefaultTenantID
				if tenantID == "" {
					tenantID = store.DefaultTenantID
				}
				user, err := s.Store.GetUserByCard(ctx, tenantID, data.EBSData.PAN)
				if err != nil {
					s.Logger.Printf("error finding user: %v", err)
					continue
				}
				data.To = user.DeviceID
				data.UserMobile = user.Mobile
				record := ebs_fields.PushDataRecord{
					UUID:           data.UUID,
					Type:           data.Type,
					Date:           data.Date,
					To:             data.To,
					Title:          data.Title,
					Body:           data.Body,
					CallToAction:   data.CallToAction,
					Phone:          data.Phone,
					IsRead:         data.IsRead,
					DeviceID:       data.DeviceID,
					UserMobile:     data.UserMobile,
					PaymentRequest: data.PaymentRequest,
				}
				_ = s.Store.CreatePushData(ctx, tenantID, &record)
				s.SendPush(ctx, data)
			}
		}
	}
}
