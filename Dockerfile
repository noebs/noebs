FROM golang:alpine 

# install git
RUN apk update && apk add --no-cache git

RUN apk add build-base

ADD https://api.github.com/repos/adonese/noebs/git/refs/heads/master version.json
RUN go get github.com/adonese/noebs

COPY /go/src/github.com/adonese/noebs /go

WORKDIR /go/noebs
RUN go build .

CMD ["/go/noebs"]

EXPOSE 8080
