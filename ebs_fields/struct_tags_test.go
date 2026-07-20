package ebs_fields

import (
	"reflect"
	"testing"
)

func TestStructsHaveNoGormTags(t *testing.T) {
	types := []struct {
		name string
		typ  reflect.Type
	}{
		{"KYC", reflect.TypeOf(KYC{})},
		{"Passport", reflect.TypeOf(Passport{})},
		{"Card", reflect.TypeOf(Card{})},
		{"EBSResponse", reflect.TypeOf(EBSResponse{})},
		{"Merchant", reflect.TypeOf(Merchant{})},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			assertNoGormTags(t, tt.typ)
		})
	}
}

func TestDBTagOverrides(t *testing.T) {
	merchantType := reflect.TypeOf(Merchant{})
	assertFieldTag(t, merchantType, "MerchantID", "db", "merchant_id")
	assertFieldTag(t, merchantType, "MerchantName", "db", "name")
	assertFieldTag(t, merchantType, "MerchantCity", "db", "city")
	assertFieldTag(t, merchantType, "MerchantMobileNumber", "db", "mobile")
	assertFieldTag(t, merchantType, "IDType", "db", "id_type")
	assertFieldTag(t, merchantType, "IDNo", "db", "id_no")
	assertFieldTag(t, merchantType, "TerminalID", "db", "-")
	assertFieldTag(t, merchantType, "PushID", "db", "push_id")
	assertFieldTag(t, merchantType, "CardNumber", "db", "card")
	assertFieldTag(t, merchantType, "Hooks", "db", "hooks")
	assertFieldTag(t, merchantType, "URL", "db", "url")

	cardType := reflect.TypeOf(Card{})
	assertFieldTag(t, cardType, "CardIdx", "db", "-")

	ebsResponseType := reflect.TypeOf(EBSResponse{})
	assertFieldTag(t, ebsResponseType, "WorkingKey", "db", "-")
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

func assertFieldTag(t *testing.T, typ reflect.Type, fieldName, key, want string) {
	t.Helper()
	field, ok := typ.FieldByName(fieldName)
	if !ok {
		t.Fatalf("field %s.%s not found", typ.Name(), fieldName)
	}
	if got := field.Tag.Get(key); got != want {
		t.Fatalf("field %s.%s tag %q = %q, want %q", typ.Name(), fieldName, key, got, want)
	}
}
