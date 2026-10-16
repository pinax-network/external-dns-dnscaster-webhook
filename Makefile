SHELL := /usr/bin/env bash

APP_NAME ?= external-dns-dnscaster-webhook
IMAGE_NAME ?= ghcr.io/pinax-network/$(APP_NAME)
VERSION ?= $(shell git describe --tags --always --dirty)
REVISION ?= $(shell git rev-parse --short HEAD)
IMAGE_TAGS ?= $(VERSION)
TAG ?= $(VERSION)
KIND_CLUSTER ?= external-dns-dnscaster
KUBE_CONTEXT := kind-$(KIND_CLUSTER)

.PHONY: test docker-build docker-push release ci dev dev-cluster dev-down help
.DEFAULT_GOAL := help

## Run all unit tests.
test:
	go test -race ./...

## Build the Docker image once and add additional tags without rebuilding.
docker-build:
	@set -euo pipefail; \
	primary_tag="$(word 1,$(IMAGE_TAGS))"; \
	echo "Building $(IMAGE_NAME):$$primary_tag"; \
	docker build \
		--build-arg VERSION="$(VERSION)" \
		--build-arg REVISION="$(REVISION)" \
		-t "$(IMAGE_NAME):$$primary_tag" \
		.; \
	for tag in $(IMAGE_TAGS); do \
		if [[ "$$tag" != "$$primary_tag" ]]; then \
			echo "Tagging $(IMAGE_NAME):$$primary_tag as $(IMAGE_NAME):$$tag"; \
			docker tag "$(IMAGE_NAME):$$primary_tag" "$(IMAGE_NAME):$$tag"; \
		fi; \
	done

## Push all configured tags to the registry.
docker-push:
	@set -euo pipefail; \
	for tag in $(IMAGE_TAGS); do \
		echo "Pushing $(IMAGE_NAME):$$tag"; \
		docker push "$(IMAGE_NAME):$$tag"; \
	done

## Create a GitHub release for TAG. Requires GH_TOKEN.
release:
	@test -n "$(GH_TOKEN)" || (echo "GH_TOKEN is required" && exit 1)
	gh release create "$(TAG)" --verify-tag --title "$(TAG)" --generate-notes

## Create the kind cluster and deploy the whole test env with Tilt.
dev: dev-cluster
	tilt up --context $(KUBE_CONTEXT)

## Create the kind cluster and its local registry. Safe to re-run.
dev-cluster:
	KIND_CLUSTER=$(KIND_CLUSTER) ./hack/kind-with-registry.sh

## Delete the examples so external-dns removes their DNScaster records
dev-down:
	-kubectl --context $(KUBE_CONTEXT) delete --recursive -f example --ignore-not-found
	-kubectl --context $(KUBE_CONTEXT) -n kube-system wait --for=delete dnsrecords.externaldns.k8s.io --all --timeout=2m
	kind delete cluster --name $(KIND_CLUSTER)

# From: https://gist.github.com/klmr/575726c7e05d8780505a
## Displays help
help:
	@echo "$$(tput sgr0)";sed -ne"/^## /{h;s/.*//;:d" -e"H;n;s/^## //;td" -e"s/:.*//;G;s/\\n## /---/;s/\\n/ /g;p;}" ${MAKEFILE_LIST}|awk -F --- -v n=$$(tput cols) -v i=15 -v a="$$(tput setaf 6)" -v z="$$(tput sgr0)" '{printf"%s%*s%s ",a,-i,$$1,z;m=split($$2,w," ");l=n-i;for(j=1;j<=m;j++){l-=length(w[j])+1;if(l<= 0){l=n-i-length(w[j])-1;printf"\n%*s ",-i," ";}printf"%s ",w[j];}printf"\n";}'
