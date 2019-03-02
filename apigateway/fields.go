package main

import "golang.org/x/crypto/bcrypt"

type UserModel struct {
	ServiceID string `binding:"required" json:"service_id"`
	Password  string `binding:"required" json:"password"`
}

func (um *UserModel) hashPassword() error{
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(um.Password), 8)
	if err != nil {
		return err
	}
	um.Password = string(hashedPassword)
	return nil
}


type ErrorResponse struct {
	Code    uint
	Message string
}
