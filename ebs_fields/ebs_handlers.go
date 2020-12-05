package ebs_fields

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
)

type ErrorResponse struct {
	ErrorDetails
}

const (
	BadRequest          = "BadRequest"
	ParsingError        = "ParsingError"
	InternalServerError = "InternalServerError"
	EBSError            = "EBSError"
)

type ErrorDetails struct {
	Message string      `json:"message"`
	Code    int         `json:"code"`
	Status  string      `json:"status"`
	Details interface{} `json:"details"`
}

type ErrDetails map[string]interface{}

func ErrorToString(e validator.FieldError) ErrDetails {
	err := make(map[string]interface{})

	switch e.Tag() {
	case "required":
		err[e.Field()] = fmt.Sprintf("this field is required")
		return err
	case "max":
		err[e.Field()] = fmt.Sprintf("this field cannot be longer than %s", e.Param())
		return err
	case "min":
		err[e.Field()] = fmt.Sprintf("this field must be longer than %s", e.Param())
		return err
	case "email":
		err[e.Field()] = fmt.Sprintf("invalid email format")
		return err
	case "len":
		err[e.Field()] = fmt.Sprintf("this field must be %s characters long", e.Param())
		return err
	case "iso8601":
		err[e.Field()] = fmt.Sprintf("Wrong datetime entered (%s). Use ISO8601 format (e.g., %s)", e.Value(), time.RFC3339)
	default:
		err[e.Field()] = fmt.Sprintf("%s is not valid", e.Field())
	}

	return err

}

// Make a new error that implements the errors interface

type customError struct {
	code    int
	message string
	status  string
}

func (c customError) Error() string {
	return fmt.Sprintf("%s", c.message)
}

var (
	ContentTypeErr            = customError{message: "Content-Type must be application/json", code: http.StatusBadGateway}
	marshalingErr             = customError{message: "unable to parse EBS response (json)", code: http.StatusBadGateway, status: "malformed_gateway_response"}
	EbsGatewayConnectivityErr = customError{message: "transaction didn't went successful", code: http.StatusBadGateway, status: "EBS_gateway_error"}
)

var EbsFailedTransaction customError
