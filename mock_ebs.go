package main

import (
	"github.com/adonese/noebs/validations"
	"math/rand"
)

func MockEbsResponse()validations.ImportantEBSFields{
	code := getEbsErrorCodes()
	n := getCodesNumber()
	chosen := rand.Intn(len(n))

	var status string

	if chosen == 0{
		status = "Successful"
	}else{
		status = "Failed"
	}

	response := validations.ImportantEBSFields{
		ResponseMessage:      code[chosen],
		ResponseStatus:       status,
		ResponseCode:         chosen,
		ReferenceNumber:      rand.Intn(9999),
		ApprovalCode:         rand.Intn(9999),
	}
	return response
}


type ebsCodes map[int]string

func getEbsErrorCodes() ebsCodes {

	code := ebsCodes{
		0: "Approval",
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
func getCodesNumber() []int{
	return []int{0,103,130,178,158,161,191,194,196,201,205,251,281,338,355,375,362,389,412,413,467}
}