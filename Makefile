PKG?=github.com/smallstep/autocert/controller
BINNAME?=autocert

# Set V to 1 for verbose output from the Makefile
Q=$(if $V,,@)
PREFIX?=
SRC=$(shell find . -type f -name '*.go' -not -path "./vendor/*")
GOOS_OVERRIDE ?=
OUTPUT_ROOT=output/

# Set shell to bash for `echo -e`
SHELL := /bin/bash

all: build lint test

.PHONY: all

#########################################
# Bootstrapping
#########################################

bootstra%:
	$Q curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin latest
	$Q go install golang.org/x/vuln/cmd/govulncheck@latest
	$Q go install gotest.tools/gotestsum@latest

#################################################
# Determine the type of `push` and `version`
#################################################

# Version flags to embed in the binaries
VERSION ?= $(shell [ -d .git ] && git describe --tags --always --dirty="-dev")
# If we are not in an active git dir then try reading the version from .VERSION.
# .VERSION contains a slug populated by `git archive`.
VERSION := $(or $(VERSION),$(shell ./.version.sh .VERSION))
VERSION := $(shell echo $(VERSION) | sed 's/^v//')
NOT_RC  := $(shell echo $(VERSION) | grep -v -e -rc)

# If TRAVIS_TAG is set then we know this ref has been tagged.
ifdef TRAVIS_TAG
	ifeq ($(NOT_RC),)
		PUSHTYPE=release-candidate
	else
		PUSHTYPE=release
	endif
else
	PUSHTYPE=master
endif

#########################################
# Build
#########################################

DATE    := $(shell date -u '+%Y-%m-%d %H:%M UTC')
LDFLAGS := -ldflags='-w -X "main.Version=$(VERSION)" -X "main.BuildTime=$(DATE)"'
GOFLAGS := CGO_ENABLED=0

download:
	$Q go mod download

build: $(PREFIX)bin/$(BINNAME)
	@echo "Build Complete!"

$(PREFIX)bin/$(BINNAME): download $(call rwildcard,*.go)
	$Q mkdir -p $(@D)
	$Q $(GOOS_OVERRIDE) $(GOFLAGS) go build -v -o $(PREFIX)bin/$(BINNAME) $(LDFLAGS) $(PKG)

# Target for building without calling dep ensure
simple:
	$Q mkdir -p bin/
	$Q $(GOOS_OVERRIDE) $(GOFLAGS) go build -v -o bin/$(BINNAME) $(LDFLAGS) $(PKG)
	@echo "Build Complete!"

.PHONY: build simple

#########################################
# Go generate
#########################################

generate:
	$Q go generate ./...

.PHONY: generate

#########################################
# Test
#########################################
test:
	$Q $(GOFLAGS) gotestsum -- -coverprofile=coverage.out -short -covermode=atomic ./...

.PHONY: test

#########################################
# Linting
#########################################

fmt:
	$Q goimports -l -w $(SRC)

lint: SHELL:=/bin/bash
lint:
	$Q LOG_LEVEL=error golangci-lint run --config <(curl -s https://raw.githubusercontent.com/smallstep/workflows/master/.golangci.yml) --timeout=30m
	$Q govulncheck ./...

.PHONY: fmt lint

#########################################
# Install
#########################################

INSTALL_PREFIX?=/usr/

install: $(PREFIX)bin/$(BINNAME)
	$Q install -D $(PREFIX)bin/$(BINNAME) $(DESTDIR)$(INSTALL_PREFIX)bin/$(BINNAME)

uninstall:
	$Q rm -f $(DESTDIR)$(INSTALL_PREFIX)/bin/$(BINNAME)

.PHONY: install uninstall

#########################################
# Clean
#########################################

clean:
ifneq ($(BINNAME),"")
	$Q rm -f bin/$(BINNAME)
endif

.PHONY: clean

#################################################
# Docker images for development
#################################################

DOCKER_DEV_TAG=docker tag smallstep/$(1):latest localhost:5000/$(1):latest
DOCKER_DEV_PUSH=docker push localhost:5000/$(1):latest

docker-dev: docker
	$(call DOCKER_DEV_TAG,autocert-controller)
	$(call DOCKER_DEV_TAG,autocert-init)
	$(call DOCKER_DEV_TAG,autocert-bootstrapper)
	$(call DOCKER_DEV_TAG,autocert-renewer)
	$(call DOCKER_DEV_PUSH,autocert-controller)
	$(call DOCKER_DEV_PUSH,autocert-init)
	$(call DOCKER_DEV_PUSH,autocert-bootstrapper)
	$(call DOCKER_DEV_PUSH,autocert-renewer)

# starts docker registry for development
docker-registry:
	$Q docker run -d -p 5000:5000 --restart=always --name registry registry:2

.PHONY: docker-dev docker-registry
