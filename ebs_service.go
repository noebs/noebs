package main

import (
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/utils"
	"github.com/sirupsen/logrus"
	chat "github.com/tutipay/ws"
	_ "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var noebsConfig ebs_fields.NoebsConfig
var logrusLogger = logrus.New()
var redisClient = utils.GetRedisClient("")
var database *gorm.DB
var consumerService consumer.Service
var service consumer.Service
var auth gateway.JWTAuth
var dashService dashboard.Service
var merchantServices = merchant.Service{}
var hub chat.Hub

func main() {

	go hub.Run()
	go consumer.BillerHooks()
	if noebsConfig.Port == "" {
		noebsConfig.Port = ":8080"
	}
	logrusLogger.Fatal(GetMainEngine().Run(noebsConfig.Port))
}
