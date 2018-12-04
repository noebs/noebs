FROM golang:alpine 

# install git
RUN apk update && apk add --no-cache git

#COPY . $GOPATH/src/


ADD https://api.github.com/repos/adonese/noebs/git/refs/heads/master version.json
RUN go get github.com/adonese/noebs

## git them
#RUN git clone https://github.com/adonese/noebs noebs
#RUN cd noebs && go get -d -v

# Build the binary
#RUN cd noebs && go build -o /go/noebs

# Build a small image

#FROM scratch

# Copy our static executable
#COPY --from=builder /app/noebs /app/noebs

# RUN noebs
CMD ["noebs"]

