root_dir:=$(shell dirname $(realpath $(lastword $(MAKEFILE_LIST))))

SRC = $(shell find . -name '*.go')
SRC_TEST = $(shell find . -name '*_test.go')

callme: $(SRC)
	go build

.PHONY: fmt
fmt: $(SRC)
	$(foreach file, $^, go fmt $(file);)

.PHONY: test
test: $(SRC_TEST)
	$(foreach file, $^, cd $(dir $(file)) && go test -coverprofile=coverage.out; cd ..;)

.PHONY: coverage
coverage: test
	$(foreach file, $^, cd $(dir $(file)) && go tool cover -html=coverage.out; cd ..;)
