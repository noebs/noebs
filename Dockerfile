FROM golang

# Create the directory where the application will reside
RUN mkdir /app
RUN apk install git

# Now CD into the /app directory, right?
# Let's install dep first
# RUN curl https://raw.githubusercontent.com/golang/dep/master/install.sh | sh
RUN go get -u github.com/golang/dep/cmd/dep

RUN cd /app
RUN git clone https://github.com/adonese/noebs
RUN dep ensure
RUN go build
ADD noebs /app

ENTRYPOINT ["./noebs"]
EXPOSE 8080
