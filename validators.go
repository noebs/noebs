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

func errorMessages(e *validator.FieldError) map[string]string {

		err := make(map[string]string)

		switch e.Tag {
		case "required":
			err[e.Field] = fmt.Sprintf("this field is required")
			return err
		case "max":
			err[e.Field] = fmt.Sprintf("this field cannot be longer than %s", e.Param)
			return err
		case "min":
			err[e.Field] = fmt.Sprintf("this field must be longer than %s",  e.Param)
			return err
		case "email":
			err[e.Field] = fmt.Sprintf("invalid email format")
			return err
		case "len":
			err[e.Field] = fmt.Sprintf("this field must be %s characters long", e.Param)
			return err
		}
		err[e.Field] = fmt.Sprintf("%s is not valid", e.Field)
		return err
	}