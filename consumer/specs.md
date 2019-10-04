# Writing consumer services

## List of services
- balance
- is_alive
- billers
    - mtn topup
    - sudani topup
    - zain topup
    - nec bill payment
    - mtn bill inquiry
    - mtn bill payment
    - sudani bill inquiry
    - sudani bill payment
    - zain bill inquiry
    - zain bill payment
- p2p (card transfer)
- purchase
- status (originalUUID the only new field)
- key (public key)

## Urls
	consumer.POST("/balance", ConsumerBalance)
	consumer.POST("/is_alive", ConsumerIsAlive)
	consumer.POST("/bill_payment", ConsumerBillPayment)
	consumer.POST("/bill_inquiry", ConsumerBillPayment)
	consumer.POST("/p2p", ConsumerCardTransfer)
	consumer.POST("/purchase", ConsumerPurchase)
	consumer.POST("/status", ConsumerStatus)
	consumer.POST("/key", ConsumerWorkingKey)

## Template engine design
*PREFERABLY*
- a base template
- to be inherited by every view (html page)
- to be rendered by a view

In short, there is no simple way to get it working without a major rewrite
to all of consumer services. And that is VERY difficult.

