build:
	@go build .

run: build
	@./noebs

test:
	@go test -v .
