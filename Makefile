BIN   := tern-ssh
PLUGIN := $(CURDIR)/plugin
PREFIX ?= $(HOME)/.local

.PHONY: build install test vet fmt plugin-link plugin-install plugin-reload check

build:
	go build -o bin/$(BIN) .

install:
	go install .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

check: fmt vet test
	go build ./...

plugin-link:
	tern plugin link $(PLUGIN)
	tern plugin reload

plugin-install:
	tern plugin install $(PLUGIN)
	tern plugin reload

plugin-reload:
	tern plugin reload
