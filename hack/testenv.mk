##@ Test environment

# The in-cluster test environment from docs/design.md: a Kind cluster, Gitea and
# the seed Job. Included from the Makefile; the fallbacks below let it run alone
# (make -f Makefile -f hack/testenv.mk ...). Every target works on the Kind
# cluster $(KIND_CLUSTER): one of the device's pool (c4factory, c4factory-2,
# ...), by default the one this checkout leased.
KIND ?= kind
KUBECTL ?= kubectl
KIND_CLUSTER ?= c4factory
CONTAINER_TOOL ?= docker
SEED_IMG ?= c4factory/seed:dev
SEED_TIMEOUT ?= 300

# Every kubectl call names the Kind context, so these targets never touch
# whatever cluster the current context happens to point at.
TESTENV_KUBECTL = $(KUBECTL) --context kind-$(KIND_CLUSTER)

# Every checkout on the device shares one Docker daemon, so a build tags
# IMAGE-$(CHECKOUT_TAG) (c4factory/operator:dev-relay-2-1a2b3c), never the
# shared IMAGE: another checkout's build can't replace it before the load.
# hack/kind.sh load puts it into $(KIND_CLUSTER) under IMAGE, the name the
# manifests use.
CHECKOUT_TAG := $(shell hack/kind.sh checkout)

# build-load IMAGE,BUILD-ARGS: build this checkout's IMAGE and load it into
# $(KIND_CLUSTER) as IMAGE.
define build-load
$(CONTAINER_TOOL) build -t $(1)-$(CHECKOUT_TAG) $(2)
hack/kind.sh load '$(KIND_CLUSTER)' $(1)
endef

# The local port Gitea is forwarded to: 3000 for c4factory, 3000+K-1 for
# c4factory-K, so each pool cluster's forward has its own.
GITEA_PORT ?= $(if $(filter c4factory-%,$(KIND_CLUSTER)),$(shell \
	expr 2999 + '$(patsubst c4factory-%,%,$(KIND_CLUSTER))' 2>/dev/null || echo 3000),3000)

.PHONY: cluster
cluster: ## Create the Kind cluster (hack/kind.yaml) if it does not exist.
	@if $(KIND) get clusters 2>/dev/null | grep -x '$(KIND_CLUSTER)' >/dev/null; then \
		echo "Kind cluster '$(KIND_CLUSTER)' already exists"; \
	else \
		hack/kind.sh keep-context $(KIND) create cluster --name '$(KIND_CLUSTER)' --config hack/kind.yaml --wait 120s; \
	fi

GITEA_ADMIN ?= c4admin
GITEA_ADMIN_EMAIL ?= c4admin@c4.factory.io

.PHONY: gitea
gitea: ## Install Gitea (config/gitea) with a generated admin password, and wait until it is ready.
	$(TESTENV_KUBECTL) create namespace gitea --dry-run=client -o yaml | $(TESTENV_KUBECTL) apply -f -
	@# The admin password is random, made once per cluster, and never written to the
	@# repository. Delete Secret gitea/gitea-admin and run this again to rotate it.
	@if ! $(TESTENV_KUBECTL) -n gitea get secret gitea-admin >/dev/null 2>&1; then \
		$(TESTENV_KUBECTL) -n gitea create secret generic gitea-admin \
			--from-literal=username='$(GITEA_ADMIN)' --from-literal=email='$(GITEA_ADMIN_EMAIL)' \
			--from-literal=password="$$(head -c 24 /dev/urandom | base64 | tr -d '\n')" && \
		$(TESTENV_KUBECTL) -n gitea label secret gitea-admin app.kubernetes.io/part-of=c4factory-testenv && \
		if $(TESTENV_KUBECTL) -n gitea get deployment gitea >/dev/null 2>&1; then \
			echo "Restarting Gitea so its admin takes the new password"; \
			$(TESTENV_KUBECTL) -n gitea rollout restart deployment/gitea; \
		fi; \
	fi
	$(TESTENV_KUBECTL) apply -k config/gitea
	$(TESTENV_KUBECTL) -n gitea rollout status deployment/gitea --timeout=300s

.PHONY: gitea-admin
gitea-admin: ## Print the Gitea admin's username and generated password, for signing in.
	@echo "Username: $$($(TESTENV_KUBECTL) -n gitea get secret gitea-admin -o jsonpath='{.data.username}' | base64 -d)"
	@echo "Password: $$($(TESTENV_KUBECTL) -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d)"

.PHONY: seed
seed: ## Seed Gitea: orgs, tagged template repos, Secret gitea-credentials.
	$(call build-load,$(SEED_IMG),-f hack/seed/Dockerfile .)
	@# Labeled, so the operator copies its shared Secrets (claude-credentials) into them.
	for ns in webshop apps; do $(TESTENV_KUBECTL) create namespace $$ns --dry-run=client -o yaml | \
		$(TESTENV_KUBECTL) label --local -f - c4.factory.io/shared-secrets=true -o yaml | $(TESTENV_KUBECTL) apply -f -; done
	$(TESTENV_KUBECTL) apply -f hack/seed/rbac.yaml
	@# Foreground: the old pods are gone before the new Job starts.
	$(TESTENV_KUBECTL) -n gitea delete job seed --ignore-not-found --cascade=foreground --wait=true
	$(TESTENV_KUBECTL) apply -f hack/seed/job.yaml
	@# Poll for Complete or Failed (kubectl wait can only watch for one of them).
	@echo "Waiting up to $(SEED_TIMEOUT)s for Job gitea/seed"; \
	state=""; deadline=$$(( $$(date +%s) + $(SEED_TIMEOUT) )); \
	while [ "$$(date +%s)" -lt "$$deadline" ]; do \
		state="$$($(TESTENV_KUBECTL) -n gitea get job seed -o jsonpath='{.status.conditions[?(@.status=="True")].type}')"; \
		case "$$state" in *Complete*|*Failed*) break ;; esac; \
		sleep 2; \
	done; \
	for pod in $$($(TESTENV_KUBECTL) -n gitea get pods -l batch.kubernetes.io/job-name=seed -o name); do \
		echo "----- logs of $$pod -----"; \
		$(TESTENV_KUBECTL) -n gitea logs "$$pod" || true; \
	done; \
	case "$$state" in \
		*Complete*) echo "Seed Job completed" ;; \
		*) echo "Seed Job did not complete (conditions: $${state:-none})" >&2; \
		   $(TESTENV_KUBECTL) -n gitea describe job seed >&2 || true; \
		   exit 1 ;; \
	esac

.PHONY: gitea-forward
gitea-forward: ## Forward Gitea to http://localhost:$(GITEA_PORT): 3000 for c4factory, 3001 for c4factory-2 (Ctrl-C to stop).
	$(TESTENV_KUBECTL) -n gitea port-forward svc/gitea $(GITEA_PORT):3000

.PHONY: cluster-delete
cluster-delete: ## Delete the Kind cluster and everything in it.
	$(KIND) delete cluster --name '$(KIND_CLUSTER)'

.PHONY: test-gitea
test-gitea: ## Run the gitprovider integration tests: against C4_GITEA_URL if set, else a local Gitea (make gitea-local).
	@if [ -n "$$C4_GITEA_URL" ]; then \
		echo "Testing against C4_GITEA_URL=$$C4_GITEA_URL"; \
	else \
		$(MAKE) --no-print-directory gitea-local && hack/gitea-local.sh fixtures && \
		set -a && . .gitea-local/credentials.env && set +a; \
	fi && \
	go test -count=1 -v ./internal/gitprovider/ -run Integration

.PHONY: test-gitea-kind
test-gitea-kind: ## Run the gitprovider integration tests against the seeded Gitea of the Kind cluster.
	@$(TESTENV_KUBECTL) -n gitea port-forward svc/gitea $(GITEA_PORT):3000 >/dev/null 2>&1 & pf=$$!; \
	trap 'kill $$pf 2>/dev/null || true' EXIT; sleep 2; \
	C4_GITEA_URL=http://localhost:$(GITEA_PORT) \
	C4_GITEA_USERNAME="$$($(TESTENV_KUBECTL) -n webshop get secret gitea-credentials -o jsonpath='{.data.username}' | base64 -d)" \
	C4_GITEA_TOKEN="$$($(TESTENV_KUBECTL) -n webshop get secret gitea-credentials -o jsonpath='{.data.token}' | base64 -d)" \
	go test -count=1 -v ./internal/gitprovider/ -run Integration

##@ Local Gitea

# The Gitea binary config/gitea runs (keep GITEA_VERSION in step with its image),
# checked against the sha256 of the release. hack/gitea-local.sh runs it from
# .gitea-local/ in this checkout, on a port of its own.
LOCALBIN ?= $(shell pwd)/bin
GITEA_VERSION ?= 1.27.3
GITEA_SHA256_amd64 = 4da93c2c10b6980c359bcb86d5573ebfd7770e2e151756534edee24c8c12d971
GITEA_SHA256_arm64 = 04c086d36dba793546e331484a9da34571763efdfa77dc526cc98e0f10917e7b
GITEA_ARCH := $(patsubst x86_64,amd64,$(patsubst aarch64,arm64,$(shell uname -m)))
GITEA_BIN ?= $(LOCALBIN)/gitea-$(GITEA_VERSION)

$(GITEA_BIN):
	@test "$$(uname -s)" = Linux -a -n "$(GITEA_SHA256_$(GITEA_ARCH))" || \
		{ echo "No Gitea checksum for $$(uname -s)/$(GITEA_ARCH); make gitea-local runs on linux amd64 or arm64" >&2; exit 1; }
	@mkdir -p "$(LOCALBIN)"
	@echo "Downloading Gitea $(GITEA_VERSION) for linux/$(GITEA_ARCH)"
	@curl -fsSL -o "$@.tmp" https://dl.gitea.com/gitea/$(GITEA_VERSION)/gitea-$(GITEA_VERSION)-linux-$(GITEA_ARCH)
	@echo "$(GITEA_SHA256_$(GITEA_ARCH))  $@.tmp" | sha256sum -c --quiet - || { rm -f "$@.tmp"; exit 1; }
	@chmod +x "$@.tmp" && mv "$@.tmp" "$@"

.PHONY: gitea-local
gitea-local: $(GITEA_BIN) ## Start this checkout's own Gitea from .gitea-local/ (no Kubernetes); credentials in .gitea-local/credentials.env.
	@GITEA="$(GITEA_BIN)" hack/gitea-local.sh start

.PHONY: seed-local
seed-local: gitea-local ## Seed the local Gitea like make seed, no kubectl: templates and tags, webshop and its token in .gitea-local/webshop.env.
	@hack/gitea-local.sh seed

.PHONY: gitea-local-stop
gitea-local-stop: ## Stop this checkout's local Gitea; its data stays in .gitea-local/.
	@hack/gitea-local.sh stop

.PHONY: kind-load
kind-load: ## Load this checkout's build of the operator image into the Kind cluster as $(IMG).
	hack/kind.sh load '$(KIND_CLUSTER)' $(IMG)

.PHONY: sample
sample: ## Apply the webshop story (config/samples/webshop).
	$(TESTENV_KUBECTL) apply -k config/samples

SESSION_IMG ?= c4factory/c4session:dev

.PHONY: session-image
session-image: ## Build the Session client image and load it into the Kind cluster.
	$(call build-load,$(SESSION_IMG),-f hack/session/Dockerfile .)

.PHONY: session
session: session-image ## Run the test Session: a pod that clones orders-api and runs c4session.
	$(TESTENV_KUBECTL) -n webshop delete pod session-orders-api --ignore-not-found --wait=true
	$(TESTENV_KUBECTL) apply -f hack/session/pod.yaml
	$(TESTENV_KUBECTL) -n webshop wait pod/session-orders-api --for=condition=Ready --timeout=120s

.PHONY: session-kill
session-kill: ## Kill the test Session's client abruptly, as a crashed devcontainer would.
	$(TESTENV_KUBECTL) -n webshop exec session-orders-api -c client -- pkill -KILL c4session

##@ Agents

GO_TOOLCHAIN_IMG ?= c4factory/go:1.26
RUNNER_GO_IMG ?= c4factory/gitea-runner-go:dev
AGENT_GO_IMG ?= c4factory/agent-go:dev
BASE_TOOLCHAIN_IMG ?= c4factory/base:dev
RUNNER_BASE_IMG ?= c4factory/gitea-runner-base:dev
AGENT_BASE_IMG ?= c4factory/agent-base:dev

.PHONY: toolchain-go
toolchain-go: ## Build the Go, Node and uv toolchain (Go and Python Containers, VS Code extensions), with a warm Go build cache.
	$(CONTAINER_TOOL) build -t $(GO_TOOLCHAIN_IMG) hack/toolchain-go

.PHONY: runner-go
runner-go: ## Build the Go and Node CI runner (label go), load it into Kind and run it.
	$(call build-load,$(RUNNER_GO_IMG),--build-arg BASE=$(GO_TOOLCHAIN_IMG) -f hack/runner/Dockerfile .)
	$(TESTENV_KUBECTL) apply -f config/runner/go.yaml
	$(TESTENV_KUBECTL) -n gitea rollout restart deployment/gitea-runner-go
	$(TESTENV_KUBECTL) -n gitea rollout status deployment/gitea-runner-go --timeout=600s

.PHONY: agent-image-go
agent-image-go: ## Build the agent image on the Go and Node toolchain and load it into Kind.
	$(call build-load,$(AGENT_GO_IMG),--build-arg BASE=$(GO_TOOLCHAIN_IMG) -f hack/agent/Dockerfile .)

.PHONY: toolchain-base
toolchain-base: ## Build the minimal toolchain (Node, uv, bash, git, make) the e2e hello-world Container and Python projects use.
	$(CONTAINER_TOOL) build -t $(BASE_TOOLCHAIN_IMG) hack/toolchain-base

.PHONY: runner-base
runner-base: ## Build the minimal CI runner (label base), load it into Kind and run it.
	$(call build-load,$(RUNNER_BASE_IMG),--build-arg BASE=$(BASE_TOOLCHAIN_IMG) -f hack/runner/Dockerfile .)
	$(TESTENV_KUBECTL) apply -f config/runner/base.yaml
	$(TESTENV_KUBECTL) -n gitea rollout restart deployment/gitea-runner-base
	$(TESTENV_KUBECTL) -n gitea rollout status deployment/gitea-runner-base --timeout=600s

.PHONY: agent-image-base
agent-image-base: ## Build the agent image on the minimal toolchain and load it into Kind.
	$(call build-load,$(AGENT_BASE_IMG),--build-arg BASE=$(BASE_TOOLCHAIN_IMG) -f hack/agent/Dockerfile .)

.PHONY: agent-sandbox
agent-sandbox: ## Install Agent Sandbox (config/agent-sandbox, v1.0.4), which runs the agents.
	$(TESTENV_KUBECTL) apply --server-side -k config/agent-sandbox
	$(TESTENV_KUBECTL) -n agent-sandbox-system rollout status deployment --timeout=300s

# Where make claude-secret stores the token: the operator's namespace, from
# which it copies Secret claude-credentials into every project namespace labeled
# c4.factory.io/shared-secrets=true (config/manager, make seed). A project of its
# own token: CLAUDE_NAMESPACE=<project>, where a Secret the operator didn't make
# is left alone.
CLAUDE_NAMESPACE ?= c4-factory-system

# The Claude token make claude-secret reads when CLAUDE_TOKEN is not given: one
# file per device, outside git, mode 0600 (docs/dev/testenv.md).
CLAUDE_TOKEN_FILE ?= $(TOOLS_ROOT)/.dev/secrets/claude-token

.PHONY: claude-secret
claude-secret: ## Store the agents' Claude token once, for every project, from CLAUDE_TOKEN or the 0600 file CLAUDE_TOKEN_FILE (.dev/secrets/claude-token).
	@KUBECTL="$(TESTENV_KUBECTL)" CLAUDE_TOKEN_FILE="$(CLAUDE_TOKEN_FILE)" hack/claude-secret.sh $(CLAUDE_NAMESPACE)

.PHONY: e2e-apps
e2e-apps: ## Apply the e2e fixture project: one hello-world Container (test/e2e/testdata/apps).
	$(TESTENV_KUBECTL) apply -k test/e2e/testdata/apps

.PHONY: metrics-server
metrics-server: ## Install metrics-server (config/metrics-server): live CPU and memory for the UI's factory view.
	$(TESTENV_KUBECTL) apply -k config/metrics-server
	$(TESTENV_KUBECTL) -n kube-system rollout status deployment/metrics-server --timeout=180s

##@ Factory lifecycle

.PHONY: up
up: ## Bring a stopped factory back: Docker, the Kind node, pods left in back-off; then make doctor.
	KIND_CLUSTER=$(KIND_CLUSTER) hack/up.sh

.PHONY: down
down: ## Stop the factory to free the machine (the Kind node keeps its state; make up resumes).
	@active=$$($(TESTENV_KUBECTL) get tasks.c4.factory.io -A -o jsonpath='{range .items[*]}{.status.phase}{"\n"}{end}' 2>/dev/null | grep -cvE '^(Merged|Failed|)$$' || true); \
	if [ "$${active:-0}" -gt 0 ]; then echo "Note: $$active Task(s) still active; they pick up where they were after make up"; fi
	$(CONTAINER_TOOL) stop $(KIND_CLUSTER)-control-plane

.PHONY: doctor
doctor: ## Check every part of the factory and say what is wrong.
	@KIND_CLUSTER=$(KIND_CLUSTER) hack/doctor.sh
