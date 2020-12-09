![CI](https://github.com/adonese/noebs/workflows/CI/badge.svg)
**Keeping' it simple**

# noebs
*Keepin' it simple*

Open source payment gateway that implements (most of) EBS services.

# About this project
This is an e-payment gateway system. It implements most of EBS's services with clear emphasis on scalabilty and a maintainable code. It is written in Go, a language for building high performant systems. It is also open source, the way any serious project should be. I wrote this software while I was learning Go, I tried to write an idiomatic Go as much as possible.

It is open source and it will remain open source. I will also maintain it and I welcome any contributors help me doing that as well.
_Our [blog post covers some other aspects about this project](https://medium.com/@adonese/noebs-a-free-and-open-source-payment-gateway-eb70c5dc26fb)_.

# Why this project
There are many reasons why I started this project. On one hand people can happily rely on EBS MCS webservices to run e.g., a POS. But this is not the goal of this project. I have a vision for the e-payment ecosystem in Sudan, way more beyond the 1SDG purchase fees.
- a middleware for a many is major entry burden. Well, you have a free one now.
- having a strong e-payment ecosystem will benefit all of us.

# How to use noebs
There are different ways to use noebs:
## Building with `go get` command [not recommended]
- make sure you have Go installed (Consult go website to see various ways to install go)[https://golang.org]
- Then
```shell
# this command may likely takes along time depending on your internet connections.
# also, make sure you are using a vpn since some of the libraries are hosted in GCE hosting which forbids Sudan
$ go get github.com/adonese/noebs
$ cd $GOPATH/github.com/adonese/noebs
$ go build .
```
You will have a binary that after running it will spawn a production ready server!

## Building using Docker and docker-compose
We provide an easier way to build and run noebs using Docker.
- Fork this repository (e.g., `git clone https://github.com/adonese/noebs`)
- `cd` to noebs root directory (E.g., $HOME/src/noebs)
- `docker build -t noebs .`  # -t for giving it a name
- `docker run -it -p 8000:8000 noebs:latest`
- Open `localhost:8000/test` in your broswer to interact with noebs

## Notes on installation
noebs needs to be connected with EBS merchant server in order to get useful responses. *However, you can run our embedded server that mocks EBS responses in cases where you cannot reach EBS server*. To do that, you need to enable the development mode using a special env var, `EBS_LOCAL_DEV`. You need to set `EBS_LOCAL_DEV=1` in order to use the mocking functionality.

- Using Docker
```shell
`docker run -it -p 8000:8000 -e EBS_LOCAL_DEV=1 noebs:latest`
```

- Using `go get` method
```shell
$ export EBS_LOCAL_DEV=1 noebs
```

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
`noebs` implements *ALL* of EBS merchant services. We are working to extend our support into other EBS services, e.g., consumer services, TITP, etc. However, those other services are not stable and some of them (consumer) are deem to deprecation.

If YOU are interested in other services, please reach out and we will be more than happy to discuss them with you.


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

**We plan on releasing our simulator very soon. Stay tuned.**

# FAQ
- Why is the name?
For no reason really. It is just the first name that came into my mind.
- Why open source?
Open source is nice! Yeey! We love open source.
- Why Go?
I was trying to learn it for awhile and I never get to actually do something useful with it. I've started this project with Python, using a framework called Sanic (very fast, Flask compatible microframework). Then I stumbled on the request validations issue:
	- i either have to write my own validation schema
	- or, just adapt another technology.
I opted to the second one. Go is cool!
- Commitment to this project?
I'm very committed to this project.
