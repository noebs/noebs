FROM golang:alpine 

# install git
RUN apk update && apk add --no-cache git

RUN apk add build-base

ADD https://api.github.com/repos/adonese/noebs/git/refs/heads/master version.json
RUN go get -u -v github.com/adonese/noebs

RUN go install github.com/adonese/noebs

ENTRYPOINT /go/bin/noebs

EXPOSE 8080
