package main

//func ConsumerRoutes(groupName string, route *gin.Engine) *gin.RouterGroup{
//
//
//	cv1 := route.Group(groupName)
//	cv1.Use(gateway.ApiAuth())
//	{
//
//		cv1.POST("/register", gateway.CreateUser)
//		cv1.POST("/refresh", gateway.RefreshHandler)
//		cv1.POST("/logout", gateway.LogOut)
//
//		cv1.POST("/balance", consumer.ConsumerBalance)
//		cv1.POST("/is_alive", consumer.ConsumerIsAlive)
//		cv1.POST("/bill_payment", consumer.ConsumerBillPayment)
//		cv1.POST("/bill_inquiry", consumer.ConsumerBillInquiry)
//		cv1.POST("/p2p", consumer.ConsumerCardTransfer)
//		cv1.POST("/purchase", consumer.ConsumerPurchase)
//		cv1.POST("/status", consumer.ConsumerStatus)
//		cv1.POST("/key", consumer.ConsumerWorkingKey)
//		cv1.POST("/ipin", consumer.ConsumerIPinChange)
//		cv1.POST("/generate_qr", consumer.QRGeneration)
//		cv1.POST("/qr_payment", consumer.QRPayment)
//		cv1.POST("/generate_ipin", consumer.ConsumerGenerateIpin)
//		cv1.POST("/complete_ipin", consumer.ConsumerCompleteIpin)
//
//		cv1.POST("/qr_refund", consumer.QRRefund)
//		cv1.GET("/mobile2pan", consumer.CardFromNumber)
//		cv1.GET("/nec2name", consumer.EelToName)
//
//		cv1.POST("/login", gateway.LoginHandler)
//		cv1.Use(gateway.AuthMiddleware())
//		cv1.GET("/get_cards", consumer.GetCards)
//		cv1.POST("/add_card", consumer.AddCards)
//
//		cv1.PUT("/edit_card", consumer.EditCard)
//		cv1.DELETE("/delete_card", consumer.RemoveCard)
//
//		cv1.GET("/get_mobile", consumer.GetMobile)
//		cv1.POST("/add_mobile", consumer.AddMobile)
//
//		cv1.POST("/test", func(c *gin.Context) {
//			c.JSON(http.StatusOK, gin.H{"message": true})
//		})
//
//	}
//
//}
