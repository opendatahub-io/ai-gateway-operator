# Include local overrides if present (gitignored).
-include local.mk

# Image URL to use all building/pushing image targets
IMG ?= ttl.sh/ai-gateway-operator-$(shell git rev-parse --short HEAD 2>/dev/null || echo dev):1h
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# CONTAINER_TOOL defines the container tool to be used for building images.
CONTAINER_TOOL ?= podman

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

## Tool Versions
CONTROLLER_GEN_VERSION ?= v0.20.1
KUSTOMIZE_VERSION      ?= v5.8.1
GOLANGCI_LINT_VERSION  ?= v2.11.4

## Tool Commands
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
KUSTOMIZE      = go run sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)
GOLANGCI_LINT  = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

KUBECTL ?= kubectl

MODULE_NAME ?= $(notdir $(CURDIR))

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint linter.
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: ## Run golangci-lint linter and perform fixes.
	$(GOLANGCI_LINT) run --fix

.PHONY: get-manifests
get-manifests: ## Download component manifests.
	./hack/scripts/get-manifests.sh

.PHONY: deps
deps: ## Tidy and verify Go module dependencies.
	go mod tidy
	go mod verify

.PHONY: test
test: manifests generate fmt vet ## Run unit tests.
	go test -ldflags "$(LDFLAGS)" $$(go list ./... | grep -v /e2e | grep -v /integration) -coverprofile cover.out

.PHONY: test-integration-run
test-integration-run: ## Run integration tests only (cluster must be prepared).
	INTEGRATION_TEST_NAMESPACE="$(INTEGRATION_TEST_NAMESPACE)" go test -ldflags "$(LDFLAGS)" ./test/integration/ -tags=integration -v -timeout 10m -failfast

.PHONY: prepare-integration
prepare-integration: manifests generate ## Clean cluster state and install CRDs for integration tests.
	$(MAKE) cleanup-integration
	$(MAKE) install

.PHONY: test-integration
test-integration: prepare-integration test-integration-run ## Run integration tests (assumes cluster is available, starts in-process manager).

.PHONY: test-e2e-run
test-e2e-run: ## Run e2e tests only (operator must already be deployed).
	OPERATOR_NAMESPACE="$(OPERATOR_NAMESPACE)" go test -ldflags "$(LDFLAGS)" ./test/e2e/ -tags=e2e -v -timeout 5m -failfast

.PHONY: test-e2e
test-e2e: cleanup-e2e deploy test-e2e-run ## Run e2e tests (cleans cluster, deploys operator, then tests).

.PHONY: cleanup-integration
cleanup-integration: ## Clean up integration test resources from the cluster.
	./hack/scripts/cleanup-integration.sh "$(INTEGRATION_TEST_NAMESPACE)"

.PHONY: cleanup-e2e
cleanup-e2e: ## Clean up e2e test resources and uninstall operator from the cluster.
	./hack/scripts/cleanup-e2e.sh "$(OPERATOR_NAMESPACE)"

##@ Build

VERSION_PKG    = github.com/opendatahub-io/ai-gateway-operator/pkg/version
VERSION       ?= 0.0.0-dev
GIT_COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_BRANCH    ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
GIT_REPO      ?= $(shell git remote get-url origin 2>/dev/null || echo unknown)
GOOS          ?= $(shell go env GOOS)
GOARCH        ?= $(shell go env GOARCH)
CGO_ENABLED   ?= 0
BIN_DIR       ?= bin
BIN_NAME      ?= manager
LDFLAGS        = -X $(VERSION_PKG).Version=$(VERSION) \
                 -X $(VERSION_PKG).Commit=$(GIT_COMMIT) \
                 -X $(VERSION_PKG).Branch=$(GIT_BRANCH) \
                 -X $(VERSION_PKG).Repo=$(GIT_REPO)

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	mkdir -p "$(BIN_DIR)"
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BIN_NAME)" cmd/main.go

.PHONY: build-bin
build-bin: ## Build manager binary only (for Containerfile; run container-prep on host first).
	mkdir -p "$(BIN_DIR)"
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BIN_NAME)" cmd/main.go

.PHONY: container-prep
container-prep: manifests generate get-manifests ## On host: regenerate code and fetch manifests before container-build.

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	ODH_MODULE_OPERATOR_MANIFESTS_PATH=config/manifests go run -ldflags "$(LDFLAGS)" ./cmd/main.go operator

.PHONY: container-build
container-build: container-prep ## Build container image with the manager.
	$(CONTAINER_TOOL) build -f Containerfile --build-arg LDFLAGS="$(LDFLAGS)" -t ${IMG} .

.PHONY: container-push
container-push: ## Push container image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

##@ Deployment

OPERATOR_NAMESPACE         ?= ai-gateway-system
INTEGRATION_TEST_NAMESPACE ?= integration-test

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: deploy
deploy: manifests ## Deploy controller to the K8s cluster via kustomize (config/default).
	@resolved_img="$$(bash ./hack/scripts/resolve-image-ref.sh "$(IMG)")"; \
		echo "Deploying image: $$resolved_img"; \
		tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
		cp -r config "$$tmp/config"; \
		( cd "$$tmp/config/manager" && $(KUSTOMIZE) edit set image controller="$$resolved_img" ); \
		$(KUSTOMIZE) build "$$tmp/config/default" | $(KUBECTL) apply -f -
	$(KUBECTL) -n $(OPERATOR_NAMESPACE) rollout status deploy/ai-gateway-operator --timeout=5m

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster.
	-$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: push-crc-image
push-crc-image: ## Push a built image to the CRC internal registry and print the pullspec.
	@bash ./hack/scripts/push-crc-image.sh "$(IMG)" "$(OPERATOR_NAMESPACE)" "$(MODULE_NAME)"

.PHONY: deploy-crc
deploy-crc: ## Build locally, push to CRC internal registry, and deploy via kustomize.
	$(MAKE) container-build
	@img="$$(bash ./hack/scripts/push-crc-image.sh "$(IMG)" "$(OPERATOR_NAMESPACE)" "$(MODULE_NAME)")"; \
		echo "Using image: $$img"; \
		$(MAKE) deploy IMG="$$img"

.PHONY: install
install: manifests ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( $(KUSTOMIZE) build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	@out="$$( $(KUSTOMIZE) build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

##@ Helpers

.PHONY: kustomize
kustomize:

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
