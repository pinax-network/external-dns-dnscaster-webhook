SHELL := /usr/bin/env bash

APP_NAME ?= external-dns-dnscaster-webhook
IMAGE_NAME ?= ghcr.io/pinax-network/$(APP_NAME)
VERSION ?= $(shell git describe --tags --always --dirty)
REVISION ?= $(shell git rev-parse --short HEAD)
IMAGE_TAGS ?= $(VERSION)
TAG ?= $(VERSION)

.PHONY: test e2e docker-build docker-push release ci

# Run all unit tests.
test:
	go test -race ./...

# Run the e2e tests: the provider runs in dry-run mode against the real
# DNScaster API, so it only reads. Requires DNSCASTER_API_KEY,
# DNSCASTER_OWNER_ID, DNSCASTER_NAMESERVER_SET_ID and DNSCASTER_E2E_ZONE.
e2e:
	go test -tags e2e -race -count=1 -run E2E ./internal/dnscaster/

# Build the Docker image once and add additional tags without rebuilding.
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

# Push all configured tags to the registry.
docker-push:
	@set -euo pipefail; \
	for tag in $(IMAGE_TAGS); do \
		echo "Pushing $(IMAGE_NAME):$$tag"; \
		docker push "$(IMAGE_NAME):$$tag"; \
	done

# Create a GitHub release for TAG. Requires GH_TOKEN.
release:
	@test -n "$(GH_TOKEN)" || (echo "GH_TOKEN is required" && exit 1)
	gh release create "$(TAG)" --verify-tag --title "$(TAG)" --generate-notes
