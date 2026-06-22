SHELL := /bin/bash

NAME := gomaxprocs-webhook
BIN  := bin/$(NAME)
GO   := go

.PHONY: all build test lint clean

all: deps build

	
deps:
	$(GO) mod download

build:
	mkdir -p bin
	$(GO) build -o $(BIN) .

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BIN)



