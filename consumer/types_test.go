package consumer

import (
	"reflect"
	"testing"

	"github.com/adonese/noebs/utils"
	"github.com/alicebob/miniredis"
)

var mr, _ = miniredis.Run()
var mockRedis = utils.GetRedisClient(mr.Addr())

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
