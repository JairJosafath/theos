# Image URL to use all building/pushing image targets. The dev tag is loaded
# straight into the Kind cluster by `make deploy`, never pushed.
IMG ?= theos/operator:dev
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

# controller-gen rewrites every file it generates, changed or not; a test's
# result in Go's cache is keyed on the mtime of each file it read, so a file
# rewritten with the same bytes gets its old mtime back (hack/keep-mtime.sh).
MANIFEST_FILES = $(wildcard config/crd/bases/*.yaml config/rbac/role.yaml)
GENERATED_GO = $(shell find api cmd internal -name 'zz_generated.*.go')

## What make manifests and make generate write; make check says when they
## changed, make check-generated fails on it (CI, after make validate-yaml).
GENERATED_PATHS = config/crd config/rbac api dist/chart/templates/crd dist/chart/templates/rbac
# What differs from the commit under them, one line a file: a changed file
# and an untracked one (a new kind's chart CRD), which git diff misses (#1705).
GENERATED_DIRTY = git status --porcelain --untracked-files=all -- $(GENERATED_PATHS)

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects, and the chart's copies of the CRDs and the manager role.
	hack/keep-mtime.sh $(MANIFEST_FILES) -- "$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="$(CONTROLLER_GEN_PATHS)" output:crd:artifacts:config=config/crd/bases
	@hack/chart-crds.sh --write
	@hack/chart-role.sh --write

.PHONY: check-generated
check-generated: ## Fail when a generated file differs from what is committed, an untracked one too: run make manifests generate and commit them (CI runs it after make validate-yaml, #1689).
	@dirty=$$($(GENERATED_DIRTY)); [ -z "$$dirty" ] || \
		{ echo "Generated files changed; run make manifests generate and commit them:"; echo "$$dirty"; exit 1; }

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	hack/keep-mtime.sh $(GENERATED_GO) -- "$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="$(CONTROLLER_GEN_PATHS)"

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

# -trimpath keeps each checkout's absolute directory out of Go's cache keys, so
# worktrees on the same code share compiled packages and test results in the
# shared GOCACHE. It sits here, not in GOFLAGS, so a debugger keeps its paths.
GO_TRIM = -trimpath

.PHONY: vet
vet: ## Run go vet against code.
	go vet $(GO_TRIM) ./...

# Heavy goals queue device-wide for one of HEAVY_JOBS slots (default 2), shared
# by every checkout, before anything builds (hack/heavy-slot.sh). The slot is
# held while this make runs; its sub-makes don't take another. With
# THEOS_HEAVY_FIRST set (land's make check under the merge turn, #1290) it takes
# the next free slot ahead of the waiters; the recipes don't inherit it.
# A plain `make check` on a tree that already passed on this device, in any
# checkout, is answered from the cache (hack/check-cache.sh) without a slot;
# FORCE=1 runs it anyway.
ifeq ($(MAKECMDGOALS)$(THEOS_HEAVY_SLOT),check)
CHECK_CACHED := $(shell hack/check-cache.sh lookup)
endif
HEAVY_GOALS = check test lint lint-fix vulncheck docker-build deploy e2e test-e2e test-templates-operator test-templates
ifneq ($(filter $(HEAVY_GOALS),$(MAKECMDGOALS)),)
ifeq ($(THEOS_HEAVY_SLOT)$(CHECK_CACHED),)
$(shell hack/heavy-slot.sh acquire $(if $(THEOS_HEAVY_FIRST),--first )$$PPID "make $(MAKECMDGOALS)" >&2)
export THEOS_HEAVY_SLOT := 1
endif
endif
unexport THEOS_HEAVY_FIRST

# TEST_TIMEOUT bounds each package's test binary. The slowest is the controller
# envtest suite, whose specs ran one after another: 286 specs in 557 s on the CI
# runner in October 2026, and 665 s in a make check beside another one, as
# HEAVY_JOBS allows (#843). 15m left that loaded run under 1.3 times its
# runtime; 20m leaves it 1.75 times, and the CI run 2 times, and still ends a
# hung spec. The suite stops itself 30 s earlier, naming the spec that was
# running; CI prints its 10 slowest specs (THEOS_SLOWEST_SPECS), so when it grows
# toward the limit the log says which specs to speed up before raising this.
TEST_TIMEOUT ?= 20m

# The envtest packages each start their own etcd and kube-apiserver, in
# parallel, and HEAVY_JOBS lets two make checks run at once: on a loaded device
# envtest's defaults (20 s for a component to start, etcd's 7 s request timeout,
# 5 s + 2 x its election timeout) fail specs no change touched (#830). Give the
# control plane 2 minutes to start, and etcd, which reads ETCD_* from the
# environment it inherits, a 10 s election timeout (25 s per request) and the
# 1 s heartbeat that goes with it; a single-member etcd still starts in about a
# second, as it fast-forwards its first election.
ENVTEST_START_TIMEOUT ?= 2m
ENVTEST_ETCD_ELECTION_TIMEOUT ?= 10000
ENVTEST_ETCD_HEARTBEAT_INTERVAL ?= 1000
ENVTEST_ENV = KUBEBUILDER_CONTROLPLANE_START_TIMEOUT=$(ENVTEST_START_TIMEOUT) \
	ETCD_ELECTION_TIMEOUT=$(ENVTEST_ETCD_ELECTION_TIMEOUT) ETCD_HEARTBEAT_INTERVAL=$(ENVTEST_ETCD_HEARTBEAT_INTERVAL)

# Go answers an unchanged package's tests from its test cache, "(cached)": the
# same test binary, flags and environment variables, and the same size and
# mtime of every file in the module the test read (the CRDs, templates/). So a
# change to hack/, docs or the UI reruns no Go test. Coverage is collected only
# when asked, COVER=1 (CI asks); locally nobody reads cover.out. FORCE=1 reruns
# every package (-count=1); go clean -testcache forgets every result.
TEST_FLAGS = -timeout $(TEST_TIMEOUT) $(if $(COVER),-coverprofile cover.out) $(if $(FORCE),-count=1)
# envtest reserves each port it hands out as a file in
# $XDG_CACHE_HOME/kubebuilder-envtest. ./dev puts XDG_CACHE_HOME in .dev/,
# inside the module, and Go tracks the listing of each directory there a test
# read, so every envtest run made the other suites' cached results stale. The
# tests get a cache home outside the module, one per device, so envtest runs in
# every checkout see each other's ports; GOCACHE stays where go env puts it.
# Under $HOME, not $TMPDIR: the relay's TMPDIR lies inside a checkout (#1326).
TEST_CACHE_HOME ?= $(HOME)/.cache/theos/test-cache

.PHONY: test
# make test tests only the packages the change since origin/main can affect,
# and every package on main, after a change to go.mod, hack/, config/, api/ and
# the like, or with TEST_ALL=1 (or FORCE=1); hack/test-packages.sh says which
# and why. Go's test cache still answers the unchanged ones among them. With
# COVER=1, cover.out covers the packages that ran, and is empty when none did.
# The controller envtest suite runs in THEOS_TEST_PROCS processes at once (default
# 2, 1 when memory is short), each a go test of part of it (hack/go-test.sh).
test: manifests generate fmt vet setup-envtest install-helm ## Run the tests of the packages the change affects (TEST_ALL=1: every package; FORCE=1 reruns cached ones, COVER=1 writes cover.out).
	@pkgs="$$(hack/test-packages.sh)" || exit; \
	if [ -z "$$pkgs" ]; then $(if $(COVER),echo 'mode: set' >cover.out;) exit 0; fi; \
	set -x; \
	$(ENVTEST_ENV) XDG_CACHE_HOME="$(TEST_CACHE_HOME)" GOCACHE="$(shell go env GOCACHE)" HELM="$(HELM)" KUBEBUILDER_ASSETS="$(shell $(ENVTEST_LOCKED) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" hack/go-test.sh $(GO_TRIM) $(TEST_FLAGS) -- $$pkgs

# The shell tests of hack/ (hack/**/*_test.sh), all at once; each fakes what it
# calls, so none needs a cluster or the network. make check runs them.
.PHONY: test-hack
test-hack: install-helm ## Run the shell tests of the scripts in hack/.
	@hack/test-hack.sh

# Renders the operator combo (go-kubebuilder, go-operator-standard, small) and
# runs its make check before and after kubebuilder create api, with this
# repository's pinned tools (hack/test-templates-operator.sh). Not part of
# make check: run it when a change touches either operator template.
.PHONY: test-templates-operator
test-templates-operator: kubebuilder kustomize controller-gen envtest ## Render the operator combo and run its make check before and after kubebuilder create api, then its make dist.
	@# GOLANGCI_LINT_BASE is defined further down, too late for a prerequisite.
	@$(MAKE) --no-print-directory "$(GOLANGCI_LINT_BASE)"
	KUBEBUILDER="$(KUBEBUILDER)" CONTROLLER_GEN="$(CONTROLLER_GEN)" KUSTOMIZE="$(KUSTOMIZE)" ENVTEST="$(ENVTEST)" \
		GOLANGCI_LINT="$(GOLANGCI_LINT_BASE)" \
		ENVTEST_ASSETS="$(shell $(ENVTEST_LOCKED) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" \
		hack/test-templates-operator.sh

# Renders the webshop's template combos (fastapi with postgres, python cli,
# react on kubernetes, node cli), the go cli, the vscode extension and every
# AWS combo, and runs each one's own make check, then kustomize build or make
# dist (hack/test-templates.sh); COMBO="node cli" runs one. Not part of make
# check: run it when a change touches one of their templates. It fetches from
# npm, PyPI and the Go module proxy; nothing calls AWS.
.PHONY: test-templates
test-templates: kustomize ## Render the template combos (webshop, go cli, vscode extension, AWS) and run their make check, kustomize build and make dist.
	@# GOLANGCI_LINT_BASE is defined further down, too late for a prerequisite.
	@$(MAKE) --no-print-directory "$(GOLANGCI_LINT_BASE)"
	KUSTOMIZE="$(KUSTOMIZE)" GOLANGCI_LINT="$(GOLANGCI_LINT_BASE)" hack/test-templates.sh

# Regenerates the go-kubebuilder Framework from the pinned kubebuilder; review
# the diff, then tag the template (hack/template-go-kubebuilder/main.go says how
# each path is sorted).
.PHONY: template-go-kubebuilder
template-go-kubebuilder: kubebuilder ## Regenerate templates/go-kubebuilder from a pinned kubebuilder init.
	go run ./hack/template-go-kubebuilder -kubebuilder "$(KUBEBUILDER)" -out templates/go-kubebuilder

# The e2e tests run against the dedicated Kind cluster $(KIND_CLUSTER), never
# another one. make e2e builds everything it needs first; it's safe to repeat.
# KIND_CLUSTER defaults to the pool cluster this checkout leased
# (hack/lib/github.sh cluster acquire), else theos.
ifeq ($(origin KIND_CLUSTER),undefined)
KIND_CLUSTER := $(or $(shell hack/lib/github.sh cluster name 2>/dev/null),theos)
endif

.PHONY: e2e
e2e: cluster metrics-server argocd gitea seed deploy session-image sample toolchain-base runner-base agent-sandbox agent-image-base e2e-apps ## Build the whole test environment in Kind, then check every milestone.
	KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v -timeout 90m

.PHONY: test-e2e
test-e2e: e2e ## Same as e2e.

# A pass is recorded under the tree as the check left it, after manifests,
# generate and fmt, so a tree with stale generated files never hits the cache.
# The UI's own check (make ui-test: typecheck, eslint, prettier, its tests) is
# part of it: the UI workflow runs only after a merge to main, so nothing else
# before a merge would see a UI change break it (#1006). ./dev setup installs
# Node on every device, a lite one too.
# validate-yaml, test-hack and ui-test each run only when their own inputs
# changed since they last passed on this device (hack/check-cache.sh run, #1285;
# docs/dev/testenv.md says what each key covers); FORCE=1 runs them anyway. The
# key is taken after manifests, generate and fmt, so the tier's sub-make skips
# manifests. A dry run (make -n) still runs a recipe line with $(MAKE) in it,
# so it goes straight to the tier and records nothing.
CHECK_UI := $(if $(wildcard ui/package.json),ui-test)
CHECK_TIERS := validate-yaml test-hack $(CHECK_UI)
check-tier-%: manifests generate fmt
	@$(if $(findstring n,$(firstword -$(MAKEFLAGS))),,hack/check-cache.sh run $* )$(MAKE) --no-print-directory -o manifests $*
# LINT_CHANGED_ONLY=1 (the factory's own CI sets it) lints only when Go can have
# changed since TEST_BASE (hack/go-changed.sh, #2058).
CHECK_LINT := $(if $(LINT_CHANGED_ONLY),lint-if-go-changed,lint)
.PHONY: lint-if-go-changed
lint-if-go-changed:
	@if hack/go-changed.sh; then $(MAKE) --no-print-directory lint; else echo "make lint: skipped, no Go file or its config changed since the base"; fi
.PHONY: check
ifeq ($(CHECK_CACHED),)
check: manifests generate fmt vet $(CHECK_LINT) $(addprefix check-tier-,$(CHECK_TIERS)) test ## Everything to run before finishing a change: generate, format, vet, lint, validate the YAML and test, the shell tests and the UI's check too.
	@dirty=$$($(GENERATED_DIRTY)); [ -z "$$dirty" ] || \
		{ echo "Generated files changed; commit them with your change:"; echo "$$dirty"; }
	@hack/check-cache.sh record
else
check:
	@echo "$(CHECK_CACHED)"
endif

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

# govulncheck reads the Go vulnerability database over the network, so it stays
# out of make check; the Vulnerabilities workflow runs it on changes to the Go
# code and the modules, and weekly. It fails on a vulnerability the code
# reaches, in the modules or in the standard library of go.mod's toolchain.
.PHONY: vulncheck
vulncheck: govulncheck ## Check the modules and the Go toolchain for known vulnerabilities the code reaches (needs the network, not in make check).
	"$(GOVULNCHECK)" ./...

# gitleaks over every commit of every ref the clone holds (#52), with the
# false positives .gitleaks.toml allowlists. The Secrets workflow runs it on
# each pull request and push to main from a full clone, so a credential in any
# commit of a pull request fails it before the merge, and the public history
# doesn't rest on a third party's app alone. Offline once installed; it reads
# only the clone.
.PHONY: secrets-scan
secrets-scan: gitleaks ## Scan the whole Git history of every ref for secrets with gitleaks (#52).
	"$(GITLEAKS)" git --log-opts="--all" --config .gitleaks.toml --redact --no-banner --exit-code 1 .

# The YAML checks of make check run offline, without a cluster
# (hack/validate-yaml.sh): samples, e2e fixtures, the kustomize builds, the chart
# renders of helm-lint, the seed and session manifests and a rendered repository.
.PHONY: validate-yaml
validate-yaml: manifests kustomize kubectl-validate k8s-openapi helm-lint ## Validate the YAML against the built-in schemas and our CRDs, offline, and check the chart's CRDs and manager role match them and its manager keeps what make chart drops.
	@KUBECTL_VALIDATE="$(KUBECTL_VALIDATE)" KUSTOMIZE="$(KUSTOMIZE)" K8S_OPENAPI="$(K8S_OPENAPI)" \
		KUBECTL_VALIDATE_K8S_VERSION="$(KUBECTL_VALIDATE_K8S_VERSION)" OUT="$(YAML_OUT)" hack/validate-yaml.sh
	@hack/chart-crds.sh
	@hack/chart-role.sh
	@hack/chart-manager.sh "$(YAML_OUT)/chart-gitea.yaml"

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build $(GO_TRIM) -o bin/manager cmd/main.go

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
	- $(CONTAINER_TOOL) buildx create --name theos-builder
	$(CONTAINER_TOOL) buildx use theos-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) $(if $(BASE_IMAGE),--build-arg BASE_IMAGE=$(BASE_IMAGE)) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm theos-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	cd config/model-proxy && "$(KUSTOMIZE)" edit set image controller=${IMG}
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
	cd config/model-proxy && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | $(TESTENV_KUBECTL) apply -f -
	@# Same tag, new image: restart so the pods pick it up.
	$(TESTENV_KUBECTL) -n theos-system rollout restart deployment/theos-controller-manager deployment/theos-model-proxy
	$(TESTENV_KUBECTL) -n theos-system rollout status deployment/theos-controller-manager --timeout=180s

.PHONY: deploy-manifests
deploy-manifests: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config, without building.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	cd config/model-proxy && "$(KUSTOMIZE)" edit set image controller=${IMG}
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
GOVULNCHECK_VERSION ?= v1.9.0
GITLEAKS_VERSION ?= v8.21.2
# The kubebuilder that make template-go-kubebuilder runs.
KUBEBUILDER_VERSION ?= v4.16.0

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
KUBECTL_VALIDATE ?= $(LOCALBIN)/kubectl-validate-$(KUBECTL_VALIDATE_VERSION)
KUBEBUILDER ?= $(LOCALBIN)/kubebuilder-$(KUBEBUILDER_VERSION)
GOVULNCHECK ?= $(LOCALBIN)/govulncheck-$(GOVULNCHECK_VERSION)
GITLEAKS ?= $(LOCALBIN)/gitleaks-$(GITLEAKS_VERSION)
GOLANGCI_LINT = $(GOLANGCI_LINT_BASE)$(if $(GOLANGCI_LINT_PLUGINS),-custom-$(GOLANGCI_LINT_PLUGINS))

# setup-envtest under a lock, so worktrees don't download the same Kubernetes
# binaries into the shared $(LOCALBIN)/k8s at once.
ENVTEST_LOCKED = $(if $(shell command -v flock),flock "$(LOCALBIN)/k8s.lock") "$(ENVTEST)"

# The newest kubectl-validate; its tag v0.0.4 predates the Kubernetes 1.35 libraries.
KUBECTL_VALIDATE_VERSION ?= v0.0.5-0.20260105161640-a97ccfaca20b
# The newest release whose built-in schemas kubectl-validate embeds. It only
# backs K8S_OPENAPI, the schemas of the envtest release, which come first. A
# release it doesn't embed makes it fetch the schemas from GitHub instead.
KUBECTL_VALIDATE_K8S_VERSION ?= 1.35
# The Kubernetes tag of the k8s.io/api version in go.mod (v0.37.0 -> v1.37.0).
K8S_OPENAPI_TAG ?= $(shell printf '%s\n' '$(call gomodver,k8s.io/api)' | sed -E 's/^v0\./v1./')
K8S_OPENAPI ?= $(LOCALBIN)/k8s-openapi-$(K8S_OPENAPI_TAG)
## Where validate-yaml and helm-lint write what they render, in this checkout
YAML_OUT ?= $(CURDIR)/bin/yaml

HELM_VERSION ?= v4.3.0
# sha256 of helm-$(HELM_VERSION)-<os>-<arch>.tar.gz, from get.helm.sh.
HELM_SHA256_linux-amd64 = 86584a54def73570558f66f5111cc53dfed56689637ae32c1201205d494f54fb
HELM_SHA256_linux-arm64 = 31c5794dd55c66a51e6b7d2e2ac7a114ae8b1de41ff1d9ba51748ac973b06a08
HELM_SHA256_darwin-amd64 = 347a784877e0e20eac865e8d1c36a80f6bb0861d6f29abd34defb6570ef95d92
HELM_SHA256_darwin-arm64 = d3870437e1e95b67f8edbde964156c84a26503f560821d40c542441658934fba
HELM_PLATFORM = $(shell go env GOOS)-$(shell go env GOARCH)
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE):
	$(call go-install-tool,$@,sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN):
	$(call go-install-tool,$@,sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest | $(LOCALBIN) ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST_LOCKED) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}
	@# setup-envtest leaves each release's directory read-only (0555), which
	@# makes git clean -ffdx, and so a self-hosted runner's checkout, fail.
	@find "$(LOCALBIN)/k8s" -type d ! -perm -u=w -exec chmod u+w {} +

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

.PHONY: govulncheck
govulncheck: $(GOVULNCHECK) ## Download govulncheck locally if necessary.
$(GOVULNCHECK):
	$(call go-install-tool,$@,golang.org/x/vuln/cmd/govulncheck,$(GOVULNCHECK_VERSION))

.PHONY: gitleaks
gitleaks: $(GITLEAKS) ## Download gitleaks locally if necessary.
$(GITLEAKS):
	$(call go-install-tool,$@,github.com/zricethezav/gitleaks/v8,$(GITLEAKS_VERSION))

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

# kubebuilder is a release binary.
.PHONY: kubebuilder
kubebuilder: $(KUBEBUILDER) ## Download kubebuilder locally if necessary.
$(KUBEBUILDER):
	$(call install-tool,$@,echo "Downloading kubebuilder $(KUBEBUILDER_VERSION)"; \
		curl -fsSL -o "$$tmp/kubebuilder" \
		"https://github.com/kubernetes-sigs/kubebuilder/releases/download/$(KUBEBUILDER_VERSION)/kubebuilder_$(shell go env GOOS)_$(shell go env GOARCH)"; \
		chmod +x "$$tmp/kubebuilder")

.PHONY: kubectl-validate
kubectl-validate: $(KUBECTL_VALIDATE) ## Download kubectl-validate locally if necessary.
$(KUBECTL_VALIDATE):
	$(call go-install-tool,$@,sigs.k8s.io/kubectl-validate,$(KUBECTL_VALIDATE_VERSION))

.PHONY: k8s-openapi
k8s-openapi: $(LOCALBIN) ## Download the built-in OpenAPI schemas of the envtest Kubernetes release.
	@hack/k8s-openapi.sh "$(K8S_OPENAPI_TAG)" "$(K8S_OPENAPI)"

# Helm is a release tarball, checked against its pinned sha256.
$(LOCALBIN)/helm-$(HELM_VERSION):
	$(call install-tool,$@,sum='$(HELM_SHA256_$(HELM_PLATFORM))'; \
		[ -n "$$sum" ] || { echo "No Helm sha256 for $(HELM_PLATFORM)" >&2; exit 1; }; \
		echo "Downloading helm $(HELM_VERSION)"; \
		curl -fsSL -o "$$tmp/helm.tgz" "https://get.helm.sh/helm-$(HELM_VERSION)-$(HELM_PLATFORM).tar.gz"; \
		got=$$( { sha256sum "$$tmp/helm.tgz" 2>/dev/null || shasum -a 256 "$$tmp/helm.tgz"; } | cut -d' ' -f1); \
		[ "$$got" = "$$sum" ] || { echo "helm $(HELM_VERSION): sha256 is $$got and not $$sum" >&2; exit 1; }; \
		tar -xzOf "$$tmp/helm.tgz" "$(HELM_PLATFORM)/helm" >"$$tmp/helm.bin"; rm "$$tmp/helm.tgz"; \
		chmod +x "$$tmp/helm.bin")

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

# Self-hosted runners for this repository's workflows, in containers on this
# device's Docker (hack/gh-runner). The workflows run on them while the
# repository variable RUNS_ON is self-hosted (docs/dev/testenv.md).
RUNNERS ?= 1
.PHONY: gh-runner
gh-runner: ## Build and start RUNNERS self-hosted GitHub Actions runners (default 1); each registers once, extra ones go.
	@RUNNERS=$(RUNNERS) hack/gh-runner/gh-runner.sh up

.PHONY: gh-runner-down
gh-runner-down: ## Stop every GitHub Actions runner of this device and remove them from GitHub.
	@hack/gh-runner/gh-runner.sh down

.PHONY: gh-runner-status
gh-runner-status: ## Show whether Docker answers, the GitHub Actions runners' containers and their status on GitHub, and what to do.
	@hack/gh-runner/gh-runner.sh status

##@ Helm Deployment

# The chart is dist/chart, generated from dist/install.yaml by kubebuilder's helm
# plugin. Its templates/ui/, templates/gitea/, templates/gitprovider/ and values.yaml are written
# by hand and survive a regeneration (docs/plans/done/helm-chart.md).
## The chart's manager template, kept by hand: the plugin regenerates it
## without the /tmp mount, POD_NAMESPACE and the shared Secrets, so make chart
## restores it, and make validate-yaml checks them (hack/chart-manager.sh, #543).
CHART_MANAGER ?= $(HELM_CHART_DIR)/templates/manager/manager.yaml
## Where make chart keeps the manager template and the plugin's diff: outside
## YAML_OUT, which make validate-yaml parses as YAML (#639).
CHART_OUT ?= $(CURDIR)/bin/chart

.PHONY: chart
chart: ## Regenerate the Helm chart in dist/chart from the kustomize output, keeping its manager template.
	@rm -f "$(YAML_OUT)/manager.kept.yaml" "$(YAML_OUT)/manager.plugin.diff"
	@mkdir -p "$(CHART_OUT)" && cp "$(CHART_MANAGER)" "$(CHART_OUT)/manager.kept.yaml"
	kubebuilder edit --plugins=helm.kubebuilder.io/v2-alpha
	@if ! diff -u --label kept --label plugin "$(CHART_OUT)/manager.kept.yaml" "$(CHART_MANAGER)" >"$(CHART_OUT)/manager.plugin.diff"; then \
		cp "$(CHART_OUT)/manager.kept.yaml" "$(CHART_MANAGER)"; \
		echo "make chart kept $(CHART_MANAGER); what the plugin would change is in $(CHART_OUT)/manager.plugin.diff, port what applies by hand"; fi

## Every option of the chart on, for the check that it renders no Secret.
HELM_ALL_ON ?= gitea.projects={apps},chat.enabled=true,chat.homeserver.enabled=true,chat.homeserver.accounts={ada},gitea.admin.existingSecret=gitea-admin,global.imagePullSecrets[0].name=ghcr,manager.imagePullSecrets[0].name=ghcr,gitProviders[0].namespace=default,gitProviders[0].url=http://git.example:3000,gitProviders[0].owner=apps,gitProviders[0].existingSecret=git-credentials,rbac.helpers.enabled=true,certManager.enabled=true,webhook.enabled=true,prometheus.enabled=true,prometheus.dashboard.enabled=true,prometheus.rules.enabled=true,networkPolicy.enabled=true,ui.allowedHosts={factory.example.com}
## Secrets the chart may render: none; a pod makes each in the cluster.
HELM_SECRETS_ALLOWED ?=

.PHONY: helm-lint
helm-lint: install-helm ## Lint the Helm chart, render it without Gitea, with it, without its catalog and Agents and with Chat, fail if it renders a Secret or an Agent on a Fable model.
	@mkdir -p "$(YAML_OUT)"
	$(HELM) lint $(HELM_CHART_DIR) --set gitea.enabled=false
	$(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --namespace $(HELM_NAMESPACE) \
		--set 'gitea.enabled=false,gitProviders[0].namespace=default,gitProviders[0].url=http://git.example:3000,gitProviders[0].owner=apps,gitProviders[0].existingSecret=git-credentials' >"$(YAML_OUT)/chart.yaml"
	$(HELM) lint $(HELM_CHART_DIR)
	$(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --namespace $(HELM_NAMESPACE) \
		--set 'gitea.projects={apps},global.imagePullSecrets[0].name=ghcr' >"$(YAML_OUT)/chart-gitea.yaml"
	$(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --namespace $(HELM_NAMESPACE) \
		--set 'gitea.projects={apps},catalog.enabled=false,agents.enabled=false,testEnvironments.browser.enabled=false,projectNamespaces.create=false,ui.enabled=false' >"$(YAML_OUT)/chart-gitea-bare.yaml"
	$(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --namespace $(HELM_NAMESPACE) \
		--set 'gitea.projects={apps},clusterDNS={169.254.20.10/32,openshift-dns/dns.operator.openshift.io/daemonset-dns=default}' >"$(YAML_OUT)/chart-gitea-dns.yaml"
	$(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --namespace $(HELM_NAMESPACE) \
		--set 'gitea.projects={apps},chat.enabled=true,chat.homeserver.enabled=true,chat.homeserver.accounts={ada}' >"$(YAML_OUT)/chart-chat.yaml"
	@if $(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --set 'gitea.projects={apps},agents.reviewer.model=claude-fable-5-1' >/dev/null 2>&1; then \
		echo "helm template renders an Agent on a Fable model; the chart must refuse it"; exit 1; fi
	@for set in 'clusterDNS=null' 'clusterDNS={8.8.8.8}' 'clusterDNS={10.0.0.0/7}' 'clusterDNS={1.2.3}' \
		'clusterDNS={kube-system/k8s-app==kube-dns}' 'clusterDNS={kube-system/a=b=c}' 'clusterDNS={Kube_System/k8s-app=kube-dns}'; do \
		if $(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --set "gitea.projects={apps},$$set" >/dev/null 2>&1; then \
			echo "helm template renders the CI fence with $$set; the chart must refuse it (#1257)"; exit 1; fi; done
	@set -o pipefail; for set in '$(HELM_ALL_ON)' 'gitea.projects={apps}'; do \
	out=$$($(HELM) template $(HELM_RELEASE) $(HELM_CHART_DIR) --namespace $(HELM_NAMESPACE) --set "$$set") || exit 1; \
	secrets=$$(echo "$$out" | awk '/^---/ { s = 0; n = ""; ns = "" } /^kind: Secret$$/ { s = 1 } \
		/^  name: / && n == "" { n = $$2 } /^  namespace: / && ns == "" { ns = $$2 } \
		s && n != "" && ns != "" { print ns "/" n; s = 0 }' | grep -vxF -e '$(HELM_SECRETS_ALLOWED)' || true); \
	if [ -n "$$secrets" ]; then \
		echo "helm template --set $$set renders a Secret; make it in the cluster instead (the secrets skill):"; \
		echo "$$secrets"; exit 1; fi; \
	done

## Helm binary to use for deploying the chart; make install-helm downloads it
HELM ?= $(LOCALBIN)/helm-$(HELM_VERSION)
## Namespace to deploy the Helm release
HELM_NAMESPACE ?= theos-system
## Name of the Helm release
HELM_RELEASE ?= theos
## Path to the Helm chart directory
HELM_CHART_DIR ?= dist/chart
## Additional arguments to pass to helm commands
HELM_EXTRA_ARGS ?=

.PHONY: install-helm
ifeq ($(HELM),$(LOCALBIN)/helm-$(HELM_VERSION))
install-helm: $(HELM) ## Download the pinned Helm if necessary, and link it as bin/helm in this checkout.
	@mkdir -p bin && ln -sf "$(HELM)" bin/helm
else
install-helm: ## Download the pinned Helm if necessary.
	@command -v "$(HELM)" >/dev/null || { echo "HELM=$(HELM) is not found" >&2; exit 1; }
endif

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
helm-uninstall: install-helm ## Uninstall the Helm release from the K8s cluster.
	$(HELM) uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-status
helm-status: install-helm ## Show Helm release status.
	$(HELM) status $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-history
helm-history: install-helm ## Show Helm release history.
	$(HELM) history $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-rollback
helm-rollback: install-helm ## Rollback to previous Helm release.
	$(HELM) rollback $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)
