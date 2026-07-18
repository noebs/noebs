package store

import (
	"reflect"
	"testing"
)

func TestInt64ArrayHandlesSQLNullAndPostgresValues(t *testing.T) {
	var values Int64Array
	if err := values.Scan(nil); err != nil {
		t.Fatalf("Scan(nil): %v", err)
	}
	if values != nil {
		t.Fatalf("Scan(nil) = %#v, want nil", values)
	}
	if err := values.Scan([]byte(`{11,22}`)); err != nil {
		t.Fatalf("Scan(array): %v", err)
	}
	if !reflect.DeepEqual(values, Int64Array{11, 22}) {
		t.Fatalf("Scan(array) = %#v, want [11 22]", values)
	}
	driverValue, err := values.Value()
	if err != nil {
		t.Fatalf("Value(): %v", err)
	}
	if driverValue != "{11,22}" {
		t.Fatalf("Value() = %#v, want {11,22}", driverValue)
	}
}
