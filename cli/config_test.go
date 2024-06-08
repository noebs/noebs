package main

import (
	"testing"

	firebase "firebase.google.com/go/v4"
)

func Test_verifyToken(t *testing.T) {
	fb, _ := getFirebase()
	type args struct {
		f     *firebase.App
		token string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{"test_firebase", args{f: fb, token: "AOO2nWWuSVpFDbTcSWI66vZ12OjGmZakPfkbYdh5Aji98hqh__SExUV2BSjegGVKJ8sqRKOoHxpIeNdWOeAahDzcRKedDmV7ZBblvyjxAtSOSsDrKoaCcAU3EvAkNhjTIMlCsGwuKwmdnsi-BRUJl4YQ_iDWToMCdAFwms141wsnfKSrdSrnQJ8cjSoZwhL064vCXs3SbBuueS5WBMQN6nGp5EZ0IG7dlrDsvUgMRDO8BE6cWSZyv-I"}, "0111493885", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verifyToken(tt.args.f, tt.args.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("verifyToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("verifyToken() = %v, want %v", got, tt.want)
			}
		})
	}
}
