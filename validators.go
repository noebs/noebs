package main

import (
	"gopkg.in/go-playground/validator.v8"
	"reflect"
)

func workingKeyStructValidators(validate *validator.Validate, structLevel *validator.StructLevel) {
	workingKey := structLevel.CurrentStruct.Interface().(WorkingKeyFields)
	if len(workingKey.TerminalID) != 8 {
		structLevel.ReportError(
			reflect.ValueOf(workingKey.TerminalID), "terminalId", "length_8", "tag_8")
	}
}
