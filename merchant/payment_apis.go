package merchant

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

func (s *Service) IsAlive(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.IsAliveEndpoint // EBS simulator endpoint url goes here

	var fields = ebs_fields.IsAliveFields{}
	// use bind to get free Form support rendering!
	// there is no practical need of using c.ShouldBindBodyWith;
	// Bind is more performant than ShouldBindBodyWith; the later copies the request body and reuse it
	// while Bind works directly on the responseBody stream.
	// More importantly, Bind smartly handles Forms rendering and validations; ShouldBindBodyWith forces you
	// into using only a *pre-specified* binding schema
	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, payload)
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, er)
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = "change me"

		// return a masked pan
		res.MaskPAN()

		// God please make it works.
		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			s.Logger.WithFields(logrus.Fields{
				"code":    err,
				"details": "Error in writing to Database",
			}).Info("Problem in transaction table committing")
		}
		if ebsErr != nil {
			// convert ebs res code to int
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

// IsAliveWrk is for testing only. We want to bypass our middleware checks and move
// up directly to ebs
// FIXME #68
func (s *Service) IsAliveWrk(c *fiber.Ctx) {
	//FIXME #69 make url embedded from struct
	url := s.NoebsConfig.MerchantIP + ebs_fields.IsAliveEndpoint
	req := strings.NewReader(`{"clientId": "ACTS", "systemTraceAuditNumber": 79, "tranDateTime": "200419085611", "terminalId": "18000377"}`)
	b, _ := json.Marshal(&req)
	ebs_fields.EBSHttpClient(url, b) // let that sink in
	jsonResponse(c, http.StatusOK, fiber.Map{"result": true})

}

func (s *Service) WorkingKey(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.WorkingKeyEndpoint // EBS simulator endpoint url goes here.
	var fields = ebs_fields.WorkingKeyFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
			return
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = "change me"
		// God please make it works.
		if err := s.Db.Create(&res.EBSResponse).Error; err != nil {
			s.Logger.WithFields(logrus.Fields{
				"code":    err.Error(),
				"details": "Error in writing to Database",
			}).Info("Problem in transaction table committing")
		}
		if ebsErr != nil {
			// convert ebs res code to int
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) Purchase(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.PurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.
	var fields = ebs_fields.PurchaseFields{}
	bindingErr := bindJSON(c, &fields)
	if bindingErr == nil {
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		// mask the pan
		res.MaskPAN()

		res.Name = "change me"
		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}
		uid := generateUUID()
		ctx := c.UserContext()
		s.Redis.HSet(ctx, fields.TerminalID+":purchase", uid, &res)
		s.Redis.Incr(ctx, fields.TerminalID+":number_purchase_transactions")
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			s.Redis.Incr(ctx, fields.TerminalID+":failed_transactions")
			jsonResponse(c, code, payload)
		} else {
			s.Redis.Incr(ctx, fields.TerminalID+":successful_transactions")
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	} else {
		if valErr, ok := bindingErr.(validator.ValidationErrors); ok {
			payload := validateRequest(valErr)
			jsonResponse(c, http.StatusBadRequest, payload)
		} else {
			jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": bindingErr.Error(), "code": "generic_error"})
		}
	}
}

func (s *Service) Balance(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.BalanceEndpoint
	var fields = ebs_fields.BalanceFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = "change me"
		// return a masked pan

		// God please make it works.
		s.Db.Table("transactions").Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) CardTransfer(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.CardTransferEndpoint

	var fields = ebs_fields.CardTransferFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.MaskPAN()

		res.Name = "change me"
		// God please make it works.
		s.Db.Table("transactions").Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}

}

func (s *Service) BillInquiry(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.BillInquiryEndpoint

	var fields = ebs_fields.BillInquiryFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) BillPayment(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.BillPaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.
	var fields = ebs_fields.BillPaymentFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = "change me"
		res.MaskPAN()
		s.Db.Create(&res.EBSResponse)
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

// TopUpPayment to perform electricity and telecos topups
func (s *Service) TopUpPayment(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.BillPrepaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.BillPaymentFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = "change me"
		res.MaskPAN()
		s.Db.Create(&res.EBSResponse)
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) ChangePIN(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.ChangePINEndpoint

	var fields = ebs_fields.ChangePINFields{}
	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.MaskPAN()

		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) CashOut(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.CashOutEndpoint // EBS simulator endpoint url goes here

	var fields = ebs_fields.CashOutFields{}
	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

// VoucherCashOut for non-card based transactions
func (s *Service) VoucherCashOut(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.VoucherCashOutWithAmountEndpoint // EBS simulator endpoint url goes here

	var fields = ebs_fields.VoucherCashOutFields{}
	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

// VoucherCashIn for non-card based transactions
func (s *Service) VoucherCashIn(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.VoucherCashInEndpoint // EBS simulator endpoint url goes here

	var fields = ebs_fields.VoucherCashInFields{}
	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

// Statement for non-card based transactions
func (s *Service) Statement(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.MiniStatementEndpoint // EBS simulator endpoint url goes here

	var fields = ebs_fields.MiniStatementFields{}
	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

// GenerateVoucher for non-card based transactions
func (s *Service) GenerateVoucher(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.GenerateVoucherEndpoint // EBS simulator endpoint url goes here
	var fields = ebs_fields.GenerateVoucherFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) CashIn(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.CashInEndpoint // EBS simulator endpoint url goes here
	var fields = ebs_fields.CashInFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) ToAccount(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.AccountTransferEndpoint // EBS simulator endpoint url goes here
	var fields = ebs_fields.AccountTransferFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) MiniStatement(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.MiniStatementEndpoint

	var fields = ebs_fields.MiniStatementFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) testAPI(c *fiber.Ctx) {

	url := s.NoebsConfig.MerchantIP + ebs_fields.WorkingKeyEndpoint // EBS simulator endpoint url goes here.
	var fields = ebs_fields.WorkingKeyFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = "change me"
		// God please make it works.
		s.Db.Create(&res.EBSResponse)
		if ebsErr != nil {
			// convert ebs res code to int
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

// Refund requests a refund for supported refund services in ebs merchant. Currnetly, it is not working
// FIXME issue #68
func (s *Service) Refund(c *fiber.Ctx) {
	url := s.NoebsConfig.MerchantIP + ebs_fields.RefundEndpoint
	var fields = ebs_fields.RefundFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, 400, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		// mask the pan
		res.MaskPAN()

		res.Name = "change me"
		// return a masked pan

		// God please make it works.
		s.Db.Table("transactions").Create(&res.EBSResponse)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, 400, fiber.Map{"code": bindingErr.Error()})
	}
}

// EBS is an EBS compatible endpoint! Well.
// it really just works as a reverse proxy with db and nothing more!
func (s *Service) EBS(c *fiber.Ctx) {
	url := c.Path()
	endpoint := strings.Split(url, "/")[2]
	ebsURL := s.NoebsConfig.MerchantIP + endpoint
	s.Logger.Printf("the url is: %v", url)

	jsonBuffer := c.Body()
	_, res, _ := ebs_fields.EBSHttpClient(ebsURL, jsonBuffer)

	res.Name = "change me"
	// God please make it works.
	s.Db.Create(&res.EBSResponse)
	jsonResponse(c, http.StatusOK, res)
}
