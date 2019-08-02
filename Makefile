build:
	go build ./cmd/...
.PHONY: build

build_version := $(shell git describe --tags --always --dirty)

version:
	echo "$(build_version)" > ./image/version.text
.PHONY: version

install: version
	go install ./cmd/...
.PHONY: install

test:
	go test ./...
.PHONY: test

lint:
	gofmt -s -l $(shell go list -f '{{ .Dir }}' ./... ) | grep ".*\.go"; if [ "$$?" = "0" ]; then exit 1; fi
	go vet ./...
.PHONY: lint

format:
	gofmt -s -w $(shell go list -f '{{ .Dir }}' ./... )
.PHONY: format

deploy:
	oc apply -n ci -f deploy/controller.yaml
	oc apply -n ci -f deploy/controller-rbac.yaml
.PHONY: deploy
