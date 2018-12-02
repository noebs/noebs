FROM golang:alpine AS builder

# install git
RUN apk update && apk add --no-cache git

COPY . $GOPATH/src/noebs/
WORKDIR $GOPATH/src/noebs/

# Fetch dependancies
## git them
RUN git clone https://github.com/adonese/noebs .
RUN go dep ensure

# Build the binary
RUN go build -o /go/bin/noebs

# Build a small image

FROM scratch

# Copy our static executable
COPY --from=builder /go/bin/noebs /go/bin/noebs

# RUN noebs
ENTRYPOINT ["/go/bin/noebs"]
EXPOSE 8080

