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

vtest:
	$(Q)for d in $$(go list ./... | grep -v vendor); do \
    echo -e "TESTS FOR: for \033[0;35m$$d\033[0m"; \
    $(GOFLAGS) go test -v -bench=. -run=. -short -coverprofile=vcoverage.out $$d; \
	out=$$?; \
	if [[ $$out -ne 0 ]]; then ret=$$out; fi;\
    rm -f profile.coverage.out; \
	done; exit $$ret;

.PHONY: test vtest

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

#########################################
# Building Docker Image
#
# Builds a dockerfile for step by building a linux version of the step-cli and
# then copying the specific binary when building the container.
#
# This ensures the container is as small as possible without having to deal
# with getting access to private repositories inside the container during build
# time.
#########################################

# XXX We put the output for the build in 'output' so we don't mess with how we
# do rule overriding from the base Makefile (if you name it 'build' it messes up
# the wildcarding).
DOCKER_OUTPUT=$(OUTPUT_ROOT)docker/

DOCKER_MAKE=V=$V GOOS_OVERRIDE='GOOS=linux GOARCH=amd64' PREFIX=$(1) make $(1)bin/$(2)
DOCKER_BUILD=$Q docker build -t smallstep/$(1):latest -f $(2) --build-arg BINPATH=$(DOCKER_OUTPUT)bin/$(1) .

docker: docker-make controller/Dockerfile init/Dockerfile bootstrapper/Dockerfile renewer/Dockerfile
	$(call DOCKER_BUILD,autocert-controller,controller/Dockerfile)
	$(call DOCKER_BUILD,autocert-init,init/Dockerfile)
	$(call DOCKER_BUILD,autocert-bootstrapper,bootstrapper/Dockerfile)
	$(call DOCKER_BUILD,autocert-renewer,renewer/Dockerfile)

docker-make:
	mkdir -p $(DOCKER_OUTPUT)
	$(call DOCKER_MAKE,$(DOCKER_OUTPUT),autocert)

.PHONY: docker docker-make

#################################################
# Releasing Docker Images
#
# Using the docker build infrastructure, this section is responsible for
# logging into docker hub and pushing the built docker containers up with the
# appropriate tags.
#################################################

DOCKER_TAG=docker tag smallstep/$(1):latest smallstep/$(1):$(2)
DOCKER_PUSH=docker push smallstep/$(1):$(2)

docker-tag:
	$(call DOCKER_TAG,autocert-controller,$(VERSION))
	$(call DOCKER_TAG,autocert-init,$(VERSION))
	$(call DOCKER_TAG,autocert-bootstrapper,$(VERSION))
	$(call DOCKER_TAG,autocert-renewer,$(VERSION))

docker-push-tag: docker-tag
	$(call DOCKER_PUSH,autocert-controller,$(VERSION))
	$(call DOCKER_PUSH,autocert-init,$(VERSION))
	$(call DOCKER_PUSH,autocert-bootstrapper,$(VERSION))
	$(call DOCKER_PUSH,autocert-renewer,$(VERSION))

docker-push-tag-latest:
	$(call DOCKER_PUSH,autocert-controller,latest)
	$(call DOCKER_PUSH,autocert-init,latest)
	$(call DOCKER_PUSH,autocert-bootstrapper,latest)
	$(call DOCKER_PUSH,autocert-renewer,latest)

# Rely on DOCKER_USERNAME and DOCKER_PASSWORD being set inside the CI or
# equivalent environment
docker-login:
	$Q docker login -u="$(DOCKER_USERNAME)" -p="$(DOCKER_PASSWORD)"

.PHONY: docker-login docker-tag docker-push-tag docker-push-tag-latest

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

#################################################
# Targets for pushing the docker images
#################################################

# For all builds we build the docker container
docker-master: docker

# For all builds with a release candidate tag
docker-release-candidate: docker-master docker-login docker-push-tag

# For all builds with a release tag
docker-release: docker-release-candidate docker-push-tag-latest

.PHONY: docker-master docker-release-candidate docker-release

#########################################
# Debian
#########################################

changelog:
	$Q echo "autocert ($(VERSION)) unstable; urgency=medium" > debian/changelog
	$Q echo >> debian/changelog
	$Q echo "  * See https://github.com/smallstep/autocert/releases" >> debian/changelog
	$Q echo >> debian/changelog
	$Q echo " -- Smallstep Labs, Inc. <techadmin@smallstep.com>  $(shell date -uR)" >> debian/changelog

debian: changelog
	$Q mkdir -p $(RELEASE); \
	OUTPUT=../autocert_*.deb; \
	rm $$OUTPUT; \
	dpkg-buildpackage -b -rfakeroot -us -uc && cp $$OUTPUT $(RELEASE)/

distclean: clean

.PHONY: changelog debian distclean

#################################################
# Build statically compiled step binary for various operating systems
#################################################

BINARY_OUTPUT=$(OUTPUT_ROOT)binary/
BUNDLE_MAKE=v=$v GOOS_OVERRIDE='GOOS=$(1) GOARCH=$(2)' PREFIX=$(3) make $(3)bin/$(BINNAME)
RELEASE=./.travis-releases

binary-linux:
	$(call BUNDLE_MAKE,linux,amd64,$(BINARY_OUTPUT)linux/)

binary-darwin:
	$(call BUNDLE_MAKE,darwin,amd64,$(BINARY_OUTPUT)darwin/)

define BUNDLE
	$(q)BUNDLE_DIR=$(BINARY_OUTPUT)$(1)/bundle; \
	stepName=autocert_$(2); \
 	mkdir -p $$BUNDLE_DIR $(RELEASE); \
	TMP=$$(mktemp -d $$BUNDLE_DIR/tmp.XXXX); \
	trap "rm -rf $$TMP" EXIT INT QUIT TERM; \
	newdir=$$TMP/$$stepName; \
	mkdir -p $$newdir/bin; \
	cp $(BINARY_OUTPUT)$(1)/bin/$(BINNAME) $$newdir/bin/; \
	cp README.md $$newdir/; \
	NEW_BUNDLE=$(RELEASE)/autocert_$(2)_$(1)_$(3).tar.gz; \
	rm -f $$NEW_BUNDLE; \
    tar -zcvf $$NEW_BUNDLE -C $$TMP $$stepName;
endef

bundle-linux: binary-linux
	$(call BUNDLE,linux,$(VERSION),amd64)

bundle-darwin: binary-darwin
	$(call BUNDLE,darwin,$(VERSION),amd64)

.PHONY: binary-linux binary-darwin bundle-linux bundle-darwin

#################################################
# Targets for creating OS specific artifacts and archives
#################################################

artifacts-linux-tag: bundle-linux # skip debian

artifacts-darwin-tag: bundle-darwin

artifacts-archive-tag:
	$Q mkdir -p $(RELEASE)
	$Q git archive v$(VERSION) | gzip > $(RELEASE)/autocert_$(VERSION).tar.gz

artifacts-tag: artifacts-linux-tag artifacts-archive-tag # skip artifacts-darwin-tag

.PHONY: artifacts-linux-tag artifacts-darwin-tag artifacts-archive-tag artifacts-tag

#################################################
# Targets for creating step artifacts
#################################################

# For all builds that are not tagged
artifacts-master:

# For all builds with a release-candidate (-rc) tag
artifacts-release-candidate: artifacts-tag

# For all builds with a release tag
artifacts-release: artifacts-tag

# This command is called by travis directly *after* a successful build
artifacts: artifacts-$(PUSHTYPE) docker-$(PUSHTYPE)

.PHONY: artifacts-master artifacts-release-candidate artifacts-release artifacts
