FROM golang:alpine

COPY . /app

# install git
RUN apk update && apk add git

RUN apk add build-base

WORKDIR /app

RUN go build

ENTRYPOINT /app/noebs

EXPOSE 8080