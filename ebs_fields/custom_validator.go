package ebs_fields

import (
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

var validatorOnce sync.Once
var validate *validator.Validate

func Validator() *validator.Validate {
	validatorOnce.Do(func() {
		validate = validator.New()
		validate.SetTagName("binding")

		err := validate.RegisterValidation("iso8601", iso8601)
		if err != nil {
			log.Fatalf("Unexpected err %v", err)
		}

		validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]

			if name == "-" {
				return ""
			}

			return name
		})
	})
	return validate
}

func ValidateStruct(obj interface{}) error {
	if kindOfData(obj) == reflect.Struct {
		if err := Validator().Struct(obj); err != nil {
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
