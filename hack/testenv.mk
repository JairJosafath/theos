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

.PHONY: test-gitea
test-gitea: ## Run the gitprovider integration test against the seeded Gitea.
	@$(TESTENV_KUBECTL) -n gitea port-forward svc/gitea 3000:3000 >/dev/null 2>&1 & pf=$$!; \
	trap 'kill $$pf' EXIT; sleep 2; \
	C4_GITEA_URL=http://localhost:3000 \
	C4_GITEA_USERNAME="$$($(TESTENV_KUBECTL) -n webshop get secret gitea-credentials -o jsonpath='{.data.username}' | base64 -d)" \
	C4_GITEA_TOKEN="$$($(TESTENV_KUBECTL) -n webshop get secret gitea-credentials -o jsonpath='{.data.token}' | base64 -d)" \
	go test -count=1 -v ./internal/gitprovider/ -run Integration
