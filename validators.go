package main

import (
	"fmt"
	"gopkg.in/go-playground/validator.v9"
)

type ErrorResponse struct {
	ResponseType ErrorDetails `json:"error"`
}

const (
	BadRequest          string = "BadRequest"
	ParsingError        string = "ParsingError"
	InternalServerError string = "InternalServerError"
)

type ErrorDetails struct {
	Message string             `json:"message"`
	Code    int                `json:"code"`
	Status  string             `json:"status"`
	Details []ValidationErrors `json:"details"`
}

type ValidationErrors map[string]string

type Response map[string]interface{}

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
	}
	err[e.Field()] = fmt.Sprintf("%s is not valid", e.Field())
	return err

}
