package gateway

import (
	"reflect"
	"testing"
)

func TestGatewayStructsHaveNoGormTags(t *testing.T) {
	assertNoGormTags(t, reflect.TypeOf(Service{}))
	assertNoGormTags(t, reflect.TypeOf(JWT{}))
}

func assertNoGormTags(t *testing.T, typ reflect.Type) {
	t.Helper()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		t.Fatalf("expected struct type, got %s", typ.Kind())
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if tag := field.Tag.Get("gorm"); tag != "" {
			t.Fatalf("field %s.%s still has gorm tag %q", typ.Name(), field.Name, tag)
		}
	}
}
