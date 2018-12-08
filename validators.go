package main

import (
	"fmt"
	"gopkg.in/go-playground/validator.v8"
)

type ResponseContract struct {
	ResponseType ResponseType
}

type ResponseType struct {
	Message string
	Code    int
}

type Response map[string]interface{}

func errorToString(e *validator.FieldError) string {

	switch e.Tag {
	case "required":
		return fmt.Sprintf("this field is required")
	case "max":
		return fmt.Sprintf("this field cannot be longer than %s", e.Param)
	case "min":
		return fmt.Sprintf("this field must be longer than %s", e.Param)
	case "email":
		return fmt.Sprintf("invalid email format")
	case "len":
		return fmt.Sprintf("this field must be %s characters long", e.Param)
	}
	return fmt.Sprintf("%s is not valid", e.Field)

}
