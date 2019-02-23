FROM golang:alpine 

# install git
RUN apk update && apk add --no-cache git

ADD https://api.github.com/repos/adonese/noebs/git/refs/heads/master version.json
RUN go get github.com/adonese/noebs

CMD ["noebs"]

