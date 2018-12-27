package main

import (
	"fmt"
	"gopkg.in/go-playground/validator.v9"
	"morsal/noebs/validations"
	"time"
)

type SuccessfulResponse struct {
	EBSResponse validations.GenericEBSResponseFields `json:"successful_transaction"`
}

type ErrorResponse struct {
	Payload ErrorDetails `json:"error"`
}

const (
	BadRequest          = "BadRequest"
	ParsingError        = "ParsingError"
	InternalServerError = "InternalServerError"
	EBSError = "EBSError"
)

type ErrorDetails struct {
	Message string             `json:"message"`
	Code    int                `json:"code"`
	Status  string             `json:"status"`
	Details interface{} `json:"details"`
}

type ValidationErrors map[string]string

func ErrorToString(e validator.FieldError) ValidationErrors {
	err := make(map[string]string)

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
