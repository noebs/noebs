package gateway

import "github.com/adonese/noebs/ebs_fields"

type Service struct {
	ebs_fields.Model
	ServiceName string
	Password    string
	JWT         JWT
	JWTID       int
}

type JWT struct {
	ebs_fields.Model
	SecretKey string
}
