package consumer

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestService_SendSMS(t *testing.T) {
	type args struct {
		c *gin.Context
	}
	tests := []struct {
		name string
		s    *Service
		args args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.SendSMS(tt.args.c)
		})
	}
}

func Test_sendSMS(t *testing.T) {
	type args struct {
		sms SMS
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := sendSMS(tt.args.sms); (err != nil) != tt.wantErr {
				t.Errorf("sendSMS() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
