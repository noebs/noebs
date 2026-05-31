package ebs_fields

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

var validatorOnce sync.Once
var validate *validator.Validate
var validateErr error

var ErrValidatorInit = errors.New("validator initialization failed")

func Validator() *validator.Validate {
	validatorOnce.Do(func() {
		v := validator.New()
		v.SetTagName("binding")

		if err := v.RegisterValidation("iso8601", iso8601); err != nil {
			validateErr = fmt.Errorf("%w: %v", ErrValidatorInit, err)
			return
		}

		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]

			if name == "-" {
				return ""
			}

			return name
		})
		validate = v
	})
	return validate
}

func ValidateStruct(obj interface{}) error {
	if kindOfData(obj) == reflect.Struct {
		v := Validator()
		if validateErr != nil {
			return validateErr
		}
		if v == nil {
			return ErrValidatorInit
		}
		if err := v.Struct(obj); err != nil {
			return err
		}
	}
	return nil
}

func kindOfData(data interface{}) reflect.Kind {

	value := reflect.ValueOf(data)
	valueType := value.Kind()

	if valueType == reflect.Ptr {
		valueType = value.Elem().Kind()
	}
	return valueType
}
