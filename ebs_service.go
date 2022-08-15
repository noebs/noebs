package main

import (
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/cards"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/utils"
	"github.com/gin-gonic/gin/binding"
	"github.com/sirupsen/logrus"
	_ "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var noebsConfig ebs_fields.NoebsConfig
var logrusLogger = logrus.New()
var redisClient = utils.GetRedisClient("")
var database *gorm.DB
var consumerService consumer.Service
var service consumer.Service
var auth gateway.JWTAuth
var cardService = cards.Service{Redis: redisClient}
var dashService dashboard.Service
var merchantServices = merchant.Service{}

func init() {
	var err error

	// Initialize database
	database, err = utils.Database("test.db")
	if err != nil {
		logrusLogger.Fatalf("error in connecting to db: %v", err)
	}

	logrusLogger.Level = logrus.DebugLevel
	logrusLogger.SetReportCaller(true)

	// Parse noebs system-level configurations
	if err = parseConfig(&noebsConfig); err != nil {
		logrusLogger.Printf("error in parsing file: %v", err)
	}

	// Initialize sentry
	// sentry.Init(sentry.ClientOptions{
	// 	Dsn: noebsConfig.Sentry,
	// 	// Set TracesSampleRate to 1.0 to capture 100%
	// 	// of transactions for performance monitoring.
	// 	// We recommend adjusting this value in production,
	// 	TracesSampleRate: 1.0,
	// })

	firebaseApp, err := getFirebase()
	// gorm debug-level logger
	database.Logger.LogMode(logger.Info)
	database.AutoMigrate(&ebs_fields.User{}, &ebs_fields.Card{}, &ebs_fields.EBSResponse{}, &ebs_fields.PaymentToken{})
	auth = gateway.JWTAuth{NoebsConfig: noebsConfig}

	auth.Init()
	binding.Validator = new(ebs_fields.DefaultValidator)
	consumerService = consumer.Service{Db: database, Redis: redisClient, NoebsConfig: noebsConfig, Logger: logrusLogger, FirebaseApp: firebaseApp, Auth: &auth}
	dashService = dashboard.Service{Redis: redisClient, Db: database}
	merchantServices = merchant.Service{Db: database, Redis: redisClient, Logger: logrusLogger, NoebsConfig: noebsConfig}
}

func main() {
	// csh := consumer.NewCashout(redisClient)
	// go csh.CashoutPub() // listener for noebs cashouts.
	go consumer.BillerHooks()
	if noebsConfig.Port == "" {
		noebsConfig.Port = ":8080"
	}
	logrusLogger.Fatal(GetMainEngine().Run(noebsConfig.Port))
}
