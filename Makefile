.PHONY: build clean generate fmt install lint test

all: build

build: generate
	go build -o bin/short  ./cmd/short

clean:
	go clean ./... 

generate: 
	go generate ./... 

fmt:
	go fmt ./... 

lint: generate
	go run golang.org/x/lint/golint -set_exit_status ./...
	go run honnef.co/go/tools/cmd/staticcheck -checks all ./...

test: generate
	go test -vet all ./...


