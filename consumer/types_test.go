package consumer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func Test_newFromBytes(t *testing.T) {
	type args struct {
		d    []byte
		code int
	}
	tests := []struct {
		name    string
		args    args
		want    response
		wantErr bool
	}{
		{"testing response - 200", args{d: []byte(`{ "ebs_response": { "pubKeyValue": "MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBANx4gKYSMv3CrWWsxdPfxDxFvl+Is/0kc1dvMI1yNWDXI3AgdI4127KMUOv7gmwZ6SnRsHX/KAM0IPRe0+Sa0vMCAwEAAQ==", "UUID": "958c8835-9f89-4f96-96a8-7430039c6323", "responseMessage": "Approved", "responseStatus": "Successful", "responseCode": 0, "tranDateTime": "200222113700" } }`), code: 200}, response{Code: 0, Response: "Approved"}, false},
		{"testing response - 200", args{d: []byte(`{ "message": "EBSError", "code": 613, "status": "EBSError", "details": { "UUID": "6cccfb54-640c-495c-8e0c-434b280937a2", "responseMessage": "DUPLICATE_TRANSACTION", "responseStatus": "Failed", "responseCode": 613, "tranDateTime": "200222113700" } }`), code: 502}, response{Code: 613, Response: "DUPLICATE_TRANSACTION"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newFromBytes(tt.args.d, tt.args.code)
			if (err != nil) != tt.wantErr {
				t.Errorf("newFromBytes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("newFromBytes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsumerRuntimeDoesNotCarryBillerCallbackDefaults(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list consumer go files: %v", err)
	}
	rejectedTokens := []string{
		"default=https://sahil2.soluspay.net",
		`form:"to,default=`,
		`form:"hooks,default=`,
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		source := string(data)
		for _, rejected := range rejectedTokens {
			if strings.Contains(source, rejected) {
				t.Fatalf("%s must not silently default biller callback fields with %q", path, rejected)
			}
		}
	}
}
