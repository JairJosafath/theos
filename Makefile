# Image URL to use all building/pushing image targets. The dev tag is loaded
# straight into the Kind cluster by `make deploy`, never pushed.
IMG ?= c4factory/operator:dev
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

# The Go code controller-gen reads. Not "./...": controller-gen loads every
# go.mod below the folder, and the module cache in .dev/ (./dev) holds thousands.
CONTROLLER_GEN_PATHS ?= ./api/...;./cmd/...;./internal/...

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="$(CONTROLLER_GEN_PATHS)" output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="$(CONTROLLER_GEN_PATHS)"

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

# Heavy goals queue device-wide for one of HEAVY_JOBS slots (default 2), shared
# by every checkout, before anything builds (hack/heavy-slot.sh). The slot is
# held while this make runs; its sub-makes don't take another.
HEAVY_GOALS = check test lint lint-fix docker-build deploy e2e test-e2e
ifneq ($(filter $(HEAVY_GOALS),$(MAKECMDGOALS)),)
ifeq ($(C4_HEAVY_SLOT),)
$(shell hack/heavy-slot.sh acquire $$PPID "make $(MAKECMDGOALS)" >&2)
export C4_HEAVY_SLOT := 1
endif
endif

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST_LOCKED) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# The e2e tests run against the dedicated Kind cluster $(KIND_CLUSTER), never
# another one. make e2e builds everything it needs first; it's safe to repeat.
# KIND_CLUSTER defaults to the pool cluster this checkout leased
# (hack/lib/github.sh cluster acquire), else c4factory.
ifeq ($(origin KIND_CLUSTER),undefined)
KIND_CLUSTER := $(or $(shell hack/lib/github.sh cluster name 2>/dev/null),c4factory)
endif

.PHONY: e2e
e2e: cluster metrics-server gitea seed deploy session-image sample toolchain-base runner-base agent-sandbox agent-image-base e2e-apps ## Build the whole test environment in Kind, then check every milestone.
	KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v -timeout 90m

.PHONY: test-e2e
test-e2e: e2e ## Same as e2e.

.PHONY: check
check: manifests generate fmt vet lint test ## Everything to run before finishing a change: generate, format, vet, lint and test.
	@git diff --quiet -- config/crd config/rbac api || \
		{ echo "Generated files changed; commit them with your change:"; git diff --stat -- config/crd config/rbac api; }

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# Override BASE_IMAGE to build from another registry, e.g.
# make docker-build IMG=<img> BASE_IMAGE=docker.io/library/golang:1.26
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build $(if $(BASE_IMAGE),--build-arg BASE_IMAGE=$(BASE_IMAGE)) -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name c4-factory-builder
	$(CONTAINER_TOOL) buildx use c4-factory-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) $(if $(BASE_IMAGE),--build-arg BASE_IMAGE=$(BASE_IMAGE)) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm c4-factory-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Build the operator, load it into the Kind cluster and deploy it there.
	@# Build this checkout's own tag and load it as $(IMG) (hack/testenv.mk), so
	@# another checkout's build can't take its place.
	$(MAKE) --no-print-directory docker-build IMG=$(IMG)-$(CHECKOUT_TAG)
	hack/kind.sh load '$(KIND_CLUSTER)' $(IMG)
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | $(TESTENV_KUBECTL) apply -f -
	@# Same tag, new image: restart so the pods pick it up.
	$(TESTENV_KUBECTL) -n c4-factory-system rollout restart deployment/c4-factory-controller-manager
	$(TESTENV_KUBECTL) -n c4-factory-system rollout status deployment/c4-factory-controller-manager --timeout=180s

.PHONY: deploy-manifests
deploy-manifests: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config, without building.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to: once per device, in the .dev/ of the
## main checkout that every worktree shares (./dev), under versioned names so
## branches pinning different versions coexist. Outside git, this checkout's .dev/.
GIT_COMMON_DIR := $(shell git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)
TOOLS_ROOT := $(or $(patsubst %/.git,%,$(filter %/.git,$(GIT_COMMON_DIR))),$(CURDIR))
LOCALBIN ?= $(TOOLS_ROOT)/.dev/make/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.22.0

#ENVTEST_VERSION is the controller-runtime version to use for setup-envtest, derived from go.mod
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v")

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.13.1

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize-$(KUSTOMIZE_VERSION)
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
ENVTEST ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)
GOLANGCI_LINT_BASE = $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
# The plugins in .custom-gcl.yml are built into the binary, so its name carries
# a checksum of that file.
GOLANGCI_LINT_PLUGINS := $(shell [ -f .custom-gcl.yml ] && cksum < .custom-gcl.yml | cut -d' ' -f1)
GOLANGCI_LINT = $(GOLANGCI_LINT_BASE)$(if $(GOLANGCI_LINT_PLUGINS),-custom-$(GOLANGCI_LINT_PLUGINS))

# setup-envtest under a lock, so worktrees don't download the same Kubernetes
# binaries into the shared $(LOCALBIN)/k8s at once.
ENVTEST_LOCKED = $(if $(shell command -v flock),flock "$(LOCALBIN)/k8s.lock") "$(ENVTEST)"

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE):
	$(call go-install-tool,$@,sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN):
	$(call go-install-tool,$@,sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST_LOCKED) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

# A phony target, not a file rule: its versioned name would run go list on
# every make, not only when envtest is needed.
.PHONY: envtest
envtest: ## Download setup-envtest locally if necessary.
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT_BASE):
	$(call go-install-tool,$@,github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
ifneq ($(GOLANGCI_LINT_PLUGINS),)
$(GOLANGCI_LINT): | $(GOLANGCI_LINT_BASE)
	$(call install-tool,$@,echo "Building custom golangci-lint with plugins..."; \
		"$(GOLANGCI_LINT_BASE)" custom --destination "$$tmp" --name golangci-lint)
endif

# install-tool makes the binary $(1) unless it exists: the shell commands $(2)
# write it alone into the directory $$tmp, and it moves under its final name
# only when complete. Worktrees share $(LOCALBIN), so a lock per tool keeps two
# makes from building the same one at once.
define install-tool
@set -e; [ ! -x "$(1)" ] || exit 0; mkdir -p "$(LOCALBIN)"; \
if command -v flock >/dev/null 2>&1; then exec 9>"$(1).lock"; flock 9; fi; \
[ ! -x "$(1)" ] || exit 0; \
tmp="$$(mktemp -d "$(1).tmp.XXXXXX")"; trap 'rm -rf "$$tmp"' EXIT; \
$(2); \
mv "$$tmp"/* "$(1)"
endef

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary, versioned
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
$(call install-tool,$(1),echo "Downloading $(2)@$(3)"; GOBIN="$$tmp" go install "$(2)@$(3)")
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

include hack/testenv.mk
include hack/ui.mk

##@ GitHub Actions runner

# A self-hosted runner for this repository's workflows, in a container on this
# device's Docker (hack/gh-runner). The workflows run on it while the repository
# variable RUNS_ON is self-hosted (docs/dev/testenv.md).
.PHONY: gh-runner
gh-runner: ## Build and start the self-hosted GitHub Actions runner; it registers once.
	@hack/gh-runner/gh-runner.sh up

.PHONY: gh-runner-down
gh-runner-down: ## Stop the GitHub Actions runner and remove it from GitHub.
	@hack/gh-runner/gh-runner.sh down

.PHONY: gh-runner-status
gh-runner-status: ## Show the GitHub Actions runner's container and its status on GitHub.
	@hack/gh-runner/gh-runner.sh status

##@ Helm Deployment

# The chart is dist/chart, generated from dist/install.yaml by kubebuilder's helm
# plugin. Its templates/ui/, templates/gitprovider/ and values.yaml are written
# by hand and survive a regeneration (docs/plans/helm-chart.md).
.PHONY: chart
chart: ## Regenerate the Helm chart in dist/chart from the kustomize output.
	kubebuilder edit --plugins=helm.kubebuilder.io/v2-alpha

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart and render it without Gitea and a GitProvider value, and with Gitea.
	$(HELM) lint $(HELM_CHART_DIR) --set gitea.enabled=false
	$(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --namespace $(HELM_NAMESPACE) \
		--set 'gitea.enabled=false,gitProviders[0].namespace=default,gitProviders[0].url=http://git.example:3000,gitProviders[0].owner=apps,gitProviders[0].token=t' >/dev/null
	$(HELM) lint $(HELM_CHART_DIR)
	$(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --namespace $(HELM_NAMESPACE) \
		--set 'gitea.projects={apps},global.imagePullSecrets[0].name=ghcr' >/dev/null

## Helm binary to use for deploying the chart
HELM ?= helm
## Namespace to deploy the Helm release
HELM_NAMESPACE ?= c4-factory-system
## Name of the Helm release
HELM_RELEASE ?= c4-factory
## Path to the Helm chart directory
HELM_CHART_DIR ?= dist/chart
## Additional arguments to pass to helm commands
HELM_EXTRA_ARGS ?=

.PHONY: install-helm
install-helm: ## Install the latest version of Helm.
	@command -v $(HELM) >/dev/null 2>&1 || { \
		echo "Installing Helm..." && \
		curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-4 | bash; \
	}

.PHONY: helm-deploy
helm-deploy: install-helm ## Deploy manager to the K8s cluster via Helm. Specify an image with IMG.
	IMG="$(IMG)"; $(HELM) upgrade --install $(HELM_RELEASE) $(HELM_CHART_DIR) \
		--namespace $(HELM_NAMESPACE) \
		--create-namespace \
		--set manager.image.repository=$${IMG%:*} \
		--set manager.image.tag=$${IMG##*:} \
		--wait \
		--timeout 10m \
		$(HELM_EXTRA_ARGS)

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall the Helm release from the K8s cluster.
	$(HELM) uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-status
helm-status: ## Show Helm release status.
	$(HELM) status $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-history
helm-history: ## Show Helm release history.
	$(HELM) history $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-rollback
helm-rollback: ## Rollback to previous Helm release.
	$(HELM) rollback $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)
