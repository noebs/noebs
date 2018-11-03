# noebs
Open source e-payment platform (backend) that implements (most) of EBS services.

# About this project
This is an e-payment gateway system. It implements most of EBS's services with clear emphasis on scalabilty and a maintainable code. It is written in Go, a language for building high performant systems. It is also open source, the way any serious project should be. I wrote this software while I was learning Go, I tried to write an idiomatic Go as much as possible.

It is open source and it will remain open source. I will also maintain it and I welcome any contributors help me doing that as well.

# Why this project
There are many reasons why I started this project. On one hand people can happily rely on EBS MCS webservices to run e.g., a POS. But this is not the goal of this project. I have a vision for the e-payment ecosystem in Sudan, way more beyond the 1SDG purchase fees.
- a middleware for a many is major entry burden. Well, you have a free one now.
- having a strong e-payment ecosystem will benefit all of us.

# This project philosophy
noebs is not meant to be a full e-payment framework (e.g., unlike Morsal). It is meant as a generic e-payment gateway system. Currently, it implements EBS services, but we might add new gateway. Being such, adapts to Unix philosophy; doing one thing and do it good. Also, with our experience with embedded devices, working with authorizations and handling all of these headers and tokens (esp. JWT ones) has proven to be challenging as simply some of the older models cannot handle lengthy headers.
You can however have this system architecture, suppose that you're building a mobile payment application system:
- a mobile application app (with its backend system, obviously). This app will encapsulates the business logic and authenticate the incoming requests.
- a chatting service. Like WeChat, where people can send their money to their friends and families, in a very friendly way.
- ecommerce platform. The idea is _not_ just to offer an epayment gateway, well, EBS offers that through their MCS web services.
- and finally the payment gateway layer which handles the payment part.
- there could be other services e.g., push notifications, SMS, 2FA and plenty of others.
- logging and the reporting system.
- rate limiting, geographical blocking and other API gateway protections.
All of these will be implemented in a microservice archiectural design pattern, and it is your decision to choose what services you want. A mobile payment provider can use our payment service inside their application whenever their users are requesting any transactions. _It is not our responsibility to authenticate your users_. This way, we can use this application in virtually any place. Our client consumers are held responsible for providing any kind of authentication for their requests.


## Services we offer
Currently, we provide these services
- Purchase
- Working Key
- Refund
There are other EBS services that we are aware of, we will add them gradually.
These services are particulary used since they're the most widely used services if we eventually have proper ecommerce businesses. You'll only need to use these services.

## Our milestones
- [x] Write this README
- [ ] Add unittestings. Ugggh.
- [ ] Add test coverage
- [ ] Use Travis CI
- [ ] Add logging
- [ ] Add DB for storing EBS's transactions

# Consultancy
While everything you see here is very and open source; we don't hide any fees or charges, we expect that some might be interested in a commercial plans. We offer our consultancy services via Gndi. We have a team with variety of proficiency, from backend engineers, mobile developers to UX/UI and QA testing engineers. Some of our team members have worked at EBS, while most of the team have a huge experience in e-payment systems.

Contact us: +249 925343834 (Mohamed Yousif) | +249 9023 00672 (Mohamed Gafar) | adonese@acts-sd.com (Mohamed Yousif)

# Our simulator and EBS services
Our team have developed an internal EBS QA test system that emulates EBS test environment. We offer our simulator as a paid service 
- very superior to that of EBS testing server. It runs on weekends. Well, 24/7, just like any server should work ¯\\_(ツ)\_/¯.
- hate EBS's bureaucracy? We do too. No need for the EBS busy servers, you can test our server at any time.
- we have two plans for the simulator: 
	- you can use our EBS simulator on your own; we won't test your services.
	- we can use our EBS simulator while we do the plan for you, the exact way EBS does. Bear in mind that our testers are highly competitive and they're all ex-EBSers.
