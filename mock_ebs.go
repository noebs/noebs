package main

import (
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/adonese/noebs/ebs_fields"
)

func MockEbsResponse(field interface{}, res *ebs_fields.GenericEBSResponseFields) {
	//code := getEbsErrorCodes()
	n := getCodesNumber()
	chosen := rand.Intn(len(n))

	var status string

	if chosen == 0 {
		status = "Successful"
	} else {
		status = "Failed"
	}

	commonFields := ebs_fields.GenericEBSResponseFields{
		ResponseMessage: "Successful",
		ResponseStatus:  status,
		ResponseCode:    0,
		ReferenceNumber: "",
		ApprovalCode:    "",
	}

	*res = commonFields
	switch field.(type) {

	case mockCardTransferResponse:
		res.ToCard = "9223421234567893212"

	case mockWorkingKeyResponse:
		fmt.Printf("in workingkey")
		key, _ := getWorkingKey()
		res.WorkingKey = key

	case mockMiniStatementResponse:

		res.MiniStatementRecords = nil
	}

	//res.ImportantEBSFields = commonFields
	*res.AdditionalAmount = 544
	//res.MiniStatementRecords = generateMiniStatement()

}

type ebsCodes map[int]string

func getEbsErrorCodes() ebsCodes {

	code := ebsCodes{
		0:   "Approval",
		103: "Format Error",
		130: "Invalid Format",
		178: "Original Request Not Found",
		158: "Invalid Processing Code",
		161: "Withdrawal Limit Exceeded",
		191: "Destination Not Available",
		194: "Duplicate Transaction",
		196: "System Error",
		201: "Contact Card Issuer",
		205: "External Decline",
		251: "Insufficient Fund",
		281: "Wrong Customer Information",
		338: "PIN Tries Limit Exceeded",
		355: "Invalid PIN",
		375: "PIN Tries Limit Reached",
		362: "Encryption error",
		389: "Invalid Terminal ID",
		412: "Invalid Transaction",
		413: "Merchant Limit Exceeded",
		467: "Invalid Amount",
	}
	return code
}

//Return the corresponding EBS Code. To be used for indexing.
func getCodesNumber() []int {
	return []int{0, 103, 130, 178, 158, 161, 191, 194, 196, 201, 205, 251, 281, 338, 355, 375, 362, 389, 412, 413, 467}
}

func getWorkingKey() (string, error) {

	return "abcdef0123456789", nil
}

func generateMiniStatement() string {
	miniStatement := make(map[string]string)

	var l []map[string]string

	for i := 0; i <= 12; i++ {
		miniStatement["operationAmount"] = fmt.Sprintf("%03d", rand.Intn(999))
		miniStatement["operationCode"] = fmt.Sprintf("%03d", rand.Intn(999))
		miniStatement["operationSign"] = generateSign()
		miniStatement["operationDate"] = generateDate()
		l = append(l, miniStatement)
	}

	s, _ := json.Marshal(l)

	return string(s)
}

func generateSign() string {
	i := rand.Intn(2)
	if i == 0 {
		return "-"
	}
	return "+"
}

func generateDate() string {
	m := rand.Intn(13)
	d := rand.Intn(32)

	return fmt.Sprintf("%02d%02d", d, m)
}

type mockPurchaseResponse struct {
	ebs_fields.ImportantEBSFields
	ebs_fields.GenericEBSResponseFields
}

type mockWorkingKeyResponse struct {
	ebs_fields.ImportantEBSFields
	ebs_fields.GenericEBSResponseFields
}

type mockMiniStatementResponse struct {
	ebs_fields.ImportantEBSFields
	ebs_fields.GenericEBSResponseFields
}

type mockCardTransferResponse struct {
	ebs_fields.ImportantEBSFields
	ebs_fields.GenericEBSResponseFields
}
