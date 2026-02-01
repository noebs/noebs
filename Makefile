build:
	@go build .

run: build
	@./noebs

test:
	@go test -v .

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint is required"; exit 1; }
	@golangci-lint run ./...

generate:
	@go generate ./...

generate-mocks:
	@go generate ./wallet/psp

generate-enums:
	@go generate ./wallet/activity ./wallet/worker
