##@ Test environment

# The in-cluster test environment from docs/design.md: a Kind cluster, Gitea and
# the seed Job. Included from the Makefile; the fallbacks below let it run alone
# (make -f Makefile -f hack/testenv.mk ...).
KIND ?= kind
KUBECTL ?= kubectl
KIND_CLUSTER ?= c4factory
CONTAINER_TOOL ?= docker
SEED_IMG ?= c4factory/seed:dev
SEED_TIMEOUT ?= 300

# Every kubectl call names the Kind context, so these targets never touch
# whatever cluster the current context happens to point at.
TESTENV_KUBECTL = $(KUBECTL) --context kind-$(KIND_CLUSTER)

.PHONY: cluster
cluster: ## Create the Kind cluster (hack/kind.yaml) if it does not exist.
	@if $(KIND) get clusters 2>/dev/null | grep -qx '$(KIND_CLUSTER)'; then \
		echo "Kind cluster '$(KIND_CLUSTER)' already exists"; \
	else \
		$(KIND) create cluster --name '$(KIND_CLUSTER)' --config hack/kind.yaml --wait 120s; \
	fi

.PHONY: gitea
gitea: ## Install Gitea (config/gitea) and wait until it is ready.
	$(TESTENV_KUBECTL) apply -k config/gitea
	$(TESTENV_KUBECTL) -n gitea rollout status deployment/gitea --timeout=300s

.PHONY: seed
seed: ## Seed Gitea: orgs, tagged template repos, Secret gitea-credentials.
	$(CONTAINER_TOOL) build -t $(SEED_IMG) -f hack/seed/Dockerfile .
	$(KIND) load docker-image $(SEED_IMG) --name '$(KIND_CLUSTER)'
	$(TESTENV_KUBECTL) create namespace webshop --dry-run=client -o yaml | $(TESTENV_KUBECTL) apply -f -
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
gitea-forward: ## Forward Gitea to http://localhost:3000 (Ctrl-C to stop).
	$(TESTENV_KUBECTL) -n gitea port-forward svc/gitea 3000:3000

.PHONY: cluster-delete
cluster-delete: ## Delete the Kind cluster and everything in it.
	$(KIND) delete cluster --name '$(KIND_CLUSTER)'
